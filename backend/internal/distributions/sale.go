package distributions

import (
	"context"
	"slices"
)

// SaleInput is a sale over the counter: staff enter an order for somebody, give it its numbers
// and, in the same go, collect the money and hand it over.
type SaleInput struct {
	DistributionID int64
	BeneficiaryID  int64
	// StaffID is who writes the sale down.
	StaffID int64
	IsExtra bool
	Choices map[string]string
	// ArticleID and Quantity say what is sold, in a distribution with articles.
	ArticleID *int64
	Quantity  int
	// Identifiers are numbers asked for by name. What is still missing of the quantity is given from
	// the next free (articles whose numbers are described).
	Identifiers []string
	// Paid collects the money in AccountID; Delivered hands the order to the person it is for.
	Paid      bool
	AccountID int64
	Delivered bool
}

// Sell enters the order, numbers it, marks it paid and hands it out, as asked. It only touches what
// the sale adds: if the person already had a row of the same article, what it had before is neither
// paid nor handed out here. Everything is checked before anything is written where it can be, and
// if a later step fails what the sale already did is undone as far as it can be, so a sale is not
// left half done.
func (s *Service) Sell(ctx context.Context, in SaleInput) (Unit, error) {
	d, err := s.repo.Get(ctx, in.DistributionID)
	if err != nil {
		return Unit{}, err
	}
	if !d.Status.Deliverable() {
		return Unit{}, ErrNotDeliverable
	}
	var article *Article
	if in.ArticleID != nil {
		for i := range d.Articles {
			if d.Articles[i].ID == *in.ArticleID {
				article = &d.Articles[i]
			}
		}
	}
	numbered := article != nil && article.HasIdentifiers
	described := numbered && len(article.IdentifierFields) > 0
	if (in.Paid || in.Delivered) && numbered && !described && len(in.Identifiers) < in.Quantity {
		// Free-text numbers cannot be given from a list: they have to be written down.
		return Unit{}, ErrIdentifiersMissing
	}
	if in.Paid && in.AccountID == 0 && s.saleHasPrice(d, article, in.IsExtra) {
		return Unit{}, ErrAccountRequired
	}
	// Only whoever may have the distribution can be sold to (a member today, or on the cutoff date).
	eligible, err := s.IsEligible(ctx, d, in.BeneficiaryID)
	if err != nil {
		return Unit{}, err
	}
	if !eligible {
		return Unit{}, ErrNotEligible
	}
	if in.Delivered {
		ok, err := s.repo.WasMemberNowOrOn(ctx, in.BeneficiaryID, d.CutoffDate)
		if err != nil {
			return Unit{}, err
		}
		if !ok {
			return Unit{}, ErrInvalidCollector
		}
	}

	// What the person had before, to count only what this sale adds and to put it back if it fails.
	var before *Unit
	if existing, err := s.repo.ListUnits(ctx, d.ID, &in.BeneficiaryID); err == nil {
		for i := range existing {
			e := existing[i]
			if e.BeneficiaryID != in.BeneficiaryID || e.IsExtra != in.IsExtra {
				continue
			}
			if (in.ArticleID != nil && e.ArticleID != nil && *e.ArticleID == *in.ArticleID) || (in.ArticleID == nil && e.ArticleID == nil) {
				before = &e
				break
			}
		}
	}

	unit, err := s.RegisterUnit(ctx, RegisterInput{
		DistributionID: d.ID, BeneficiaryID: in.BeneficiaryID, RegisteredByID: in.StaffID,
		IsExtra: in.IsExtra, Choices: in.Choices, ArticleID: in.ArticleID, Quantity: in.Quantity,
		Staff: true,
	})
	if err != nil {
		return Unit{}, err
	}

	var paid, numbersAdded []string
	paidCount := 0
	didPay := false
	undo := func() {
		// Best effort: the error that made the sale fail is the one that is reported.
		if didPay {
			_ = s.repo.ClearPaid(ctx, d.ID, unit.ID, paidCount, paid, in.StaffID, in.AccountID)
		}
		if len(numbersAdded) > 0 {
			_, _ = s.RemoveIdentifiers(ctx, d.ID, unit.ID, numbersAdded)
		}
		switch {
		case before == nil:
			_ = s.repo.DeleteUnit(ctx, d.ID, unit.ID)
		case article != nil:
			_, _ = s.repo.UpdateQuantity(ctx, d.ID, unit.ID, before.Quantity)
		}
	}

	// The items this sale is about: the numbers it adds, or how many of a counted row.
	var sold []string
	if numbered {
		typed, err := expandTokens(d, unit, in.Identifiers)
		if err != nil {
			undo()
			return Unit{}, err
		}
		have := slices.Clone(unit.Identifiers)
		if len(typed) > 0 {
			if unit, err = s.AddIdentifiers(ctx, d.ID, unit.ID, typed); err != nil {
				undo()
				return Unit{}, err
			}
		}
		if missing := in.Quantity - len(typed); missing > 0 && described {
			if unit, err = s.assignNumbers(ctx, d, unit, len(unit.Identifiers)+missing); err != nil {
				undo()
				return Unit{}, err
			}
		}
		for _, identifier := range unit.Identifiers {
			if !slices.Contains(have, identifier) {
				sold = append(sold, identifier)
			}
		}
		numbersAdded = sold
	}
	quantity := 0 // the whole of a unit that is not a row of an article
	if article != nil {
		quantity = in.Quantity
	}

	if in.Paid && s.saleHasPrice(d, article, in.IsExtra) {
		p := Payment{UnitID: unit.ID, Quantity: quantity, Identifiers: sold}
		if numbered {
			p.Quantity = 0
		}
		if _, err := s.repo.MarkPaid(ctx, d.ID, []Payment{p}, in.StaffID, in.AccountID); err != nil {
			undo()
			return Unit{}, err
		}
		didPay, paid, paidCount = true, sold, p.Quantity
	}
	if in.Delivered {
		dl := Delivery{UnitID: unit.ID, Quantity: quantity, Identifiers: sold}
		if numbered {
			dl.Quantity = 0
		}
		if _, err := s.repo.Deliver(ctx, d.ID, []Delivery{dl}, in.StaffID, in.BeneficiaryID); err != nil {
			undo()
			return Unit{}, err
		}
	}
	return s.repo.GetUnit(ctx, d.ID, unit.ID)
}

// saleHasPrice says whether what is sold costs anything, so there is money to collect.
func (s *Service) saleHasPrice(d Distribution, article *Article, isExtra bool) bool {
	switch {
	case article != nil:
		return article.Price > 0
	case isExtra:
		return d.ExtraPrice != nil && *d.ExtraPrice > 0
	}
	return d.Price > 0
}
