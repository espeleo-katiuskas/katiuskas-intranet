package budgets

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
)

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

func validYear(year int) bool { return year >= 1900 && year <= 2200 }

func percent(actual, amount float64) *float64 {
	if amount == 0 {
		return nil
	}
	p := math.Round(actual/amount*10000) / 100
	return &p
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

// Get builds the budget of a year with what was really received or spent. An income account's
// actual is minus the sum of its entries (money leaves it when it is earned), an expense account's
// is the sum.
func (s *Service) Get(ctx context.Context, year int) (Budget, error) {
	if !validYear(year) {
		return Budget{}, ErrInvalidYear
	}
	items, err := s.repo.stored(ctx, year)
	if err != nil {
		return Budget{}, err
	}
	ledger, err := s.repo.ledger(ctx)
	if err != nil {
		return Budget{}, err
	}
	net, err := s.repo.netByAccount(ctx, year)
	if err != nil {
		return Budget{}, err
	}
	actualOf := func(kind Kind, id int64) float64 {
		if kind == KindIncome {
			return -net[id]
		}
		return net[id]
	}
	b := Budget{Year: year, UnassignedAccounts: []UnassignedAccount{}}
	b.Income.Items, b.Expenses.Items = []Item{}, []Item{}
	covered := map[int64]bool{}
	for _, it := range items {
		out := Item{Name: it.Name, Amount: it.Amount, Accounts: []AccountRef{}}
		for _, id := range it.AccountIDs {
			covered[id] = true
			out.Actual += actualOf(it.Kind, id)
			if a, ok := ledger[id]; ok {
				out.Accounts = append(out.Accounts, a.AccountRef)
			}
		}
		out.Actual = round2(out.Actual)
		out.Percent = percent(out.Actual, out.Amount)
		part := &b.Income
		if it.Kind == KindExpense {
			part = &b.Expenses
		}
		part.Items = append(part.Items, out)
		part.Amount += out.Amount
		part.Actual += out.Actual
	}
	for _, p := range []*Part{&b.Income, &b.Expenses} {
		p.Amount, p.Actual = round2(p.Amount), round2(p.Actual)
		p.Percent = percent(p.Actual, p.Amount)
	}
	for id, a := range ledger {
		kind, ok := budgetableKind(a)
		if !ok || covered[id] {
			continue
		}
		b.UnassignedAccounts = append(b.UnassignedAccounts, UnassignedAccount{
			AccountRef: a.AccountRef, Kind: kind, Actual: round2(actualOf(kind, id)),
		})
	}
	sort.Slice(b.UnassignedAccounts, func(i, j int) bool {
		return b.UnassignedAccounts[i].ID < b.UnassignedAccounts[j].ID
	})
	return b, nil
}

// budgetableKind says which part of a budget an account goes in: only leaf accounts whose
// effective type is income or expense do.
func budgetableKind(a ledgerAccount) (Kind, bool) {
	if a.IsGroup {
		return "", false
	}
	switch a.EffectiveType {
	case "income":
		return KindIncome, true
	case "expense":
		return KindExpense, true
	}
	return "", false
}

// List gives every year that has a budget, newest first, with its totals.
func (s *Service) List(ctx context.Context) ([]Summary, error) {
	years, err := s.repo.years(ctx)
	if err != nil {
		return nil, err
	}
	out := []Summary{}
	for _, y := range years {
		b, err := s.Get(ctx, y)
		if err != nil {
			return nil, err
		}
		out = append(out, Summary{
			Year:         y,
			IncomeAmount: b.Income.Amount, IncomeActual: b.Income.Actual, IncomePercent: b.Income.Percent,
			ExpensesAmount: b.Expenses.Amount, ExpensesActual: b.Expenses.Actual, ExpensesPercent: b.Expenses.Percent,
		})
	}
	return out, nil
}

// MissingAccountsError carries the names of the accounts a budget leaves out.
type MissingAccountsError struct{ Names []string }

func (e *MissingAccountsError) Error() string {
	return fmt.Sprintf("%s: %s", ErrAccountsMissing, strings.Join(e.Names, ", "))
}

func (e *MissingAccountsError) Unwrap() error { return ErrAccountsMissing }

// UnbalancedError carries both totals, in cents.
type UnbalancedError struct{ IncomeCents, ExpenseCents int64 }

func (e *UnbalancedError) Error() string {
	return fmt.Sprintf("%s (income %.2f, expenses %.2f)", ErrUnbalanced,
		float64(e.IncomeCents)/100, float64(e.ExpenseCents)/100)
}

func (e *UnbalancedError) Unwrap() error { return ErrUnbalanced }

// Save validates and stores the budget of a year (creating it or replacing it whole).
func (s *Service) Save(ctx context.Context, year int, in Input) (Budget, error) {
	if !validYear(year) {
		return Budget{}, ErrInvalidYear
	}
	ledger, err := s.repo.ledger(ctx)
	if err != nil {
		return Budget{}, err
	}
	used := map[int64]bool{}
	totals := map[Kind]int64{}
	for _, part := range []struct {
		kind  Kind
		items []ItemInput
	}{{KindIncome, in.Income}, {KindExpense, in.Expenses}} {
		names := map[string]bool{}
		for _, it := range part.items {
			key := strings.ToLower(strings.TrimSpace(it.Name))
			if key == "" || it.Cents < 0 {
				return Budget{}, ErrInvalidItem
			}
			if names[key] {
				return Budget{}, fmt.Errorf("%w: %s", ErrDuplicateItem, it.Name)
			}
			names[key] = true
			if len(it.AccountIDs) == 0 {
				return Budget{}, fmt.Errorf("%w: %s", ErrItemWithoutAccount, it.Name)
			}
			totals[part.kind] += it.Cents
			for _, id := range it.AccountIDs {
				a, ok := ledger[id]
				if !ok {
					return Budget{}, fmt.Errorf("%w: %d", ErrUnknownAccount, id)
				}
				kind, budgetable := budgetableKind(a)
				if !budgetable && a.IsGroup {
					return Budget{}, fmt.Errorf("%w: %s", ErrNotBudgetable, a.Name)
				}
				if !budgetable || kind != part.kind {
					return Budget{}, fmt.Errorf("%w: %s", ErrTypeMismatch, a.Name)
				}
				if used[id] {
					return Budget{}, fmt.Errorf("%w: %s", ErrDuplicateAccount, a.Name)
				}
				used[id] = true
			}
		}
	}
	missing := []string{}
	for id, a := range ledger {
		if _, ok := budgetableKind(a); ok && !a.Hidden && !used[id] {
			missing = append(missing, a.Name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return Budget{}, &MissingAccountsError{Names: missing}
	}
	if totals[KindIncome] != totals[KindExpense] {
		return Budget{}, &UnbalancedError{IncomeCents: totals[KindIncome], ExpenseCents: totals[KindExpense]}
	}
	if err := s.repo.replace(ctx, year, in); err != nil {
		return Budget{}, err
	}
	return s.Get(ctx, year)
}

func (s *Service) Delete(ctx context.Context, year int) error {
	if !validYear(year) {
		return ErrInvalidYear
	}
	return s.repo.delete(ctx, year)
}
