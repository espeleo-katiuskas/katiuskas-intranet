package places

import (
	"context"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/access"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/accounts"
)

// Who did something is an account of the catalogue (internal/accounts), made the first time a person
// acts: the places keep its id and show its name, and never read how the intranet stores people.

// AccountOf is the account of the viewer, made if it is the first time they act. It is what a route that
// changes something asks for before it does.
func (s *Service) AccountOf(ctx context.Context, viewer access.Viewer) (accounts.Account, error) {
	return s.accounts.Ensure(ctx, viewer)
}

// HasAuthored says whether the person that the issuer knows by that subject made anything in the
// catalogue of places (users.AuthorGuard). One who never acted has no account, and made nothing.
func (s *Service) HasAuthored(ctx context.Context, issuer, subject string) (bool, error) {
	account, found, err := s.accounts.Of(ctx, issuer, subject)
	if err != nil || !found {
		return false, err
	}
	return s.repo.HasAuthored(ctx, account.ID)
}

// decoratePlace adds to a place what the catalogue does not keep in its own row: how many activities went
// there. It takes the error of the call that produced the place, so that a repository call can be
// returned through it directly.
func (s *Service) decoratePlace(ctx context.Context, p Place, err error) (Place, error) {
	if err != nil {
		return Place{}, err
	}
	if err := s.countActivities(ctx, &p.ListItem); err != nil {
		return Place{}, err
	}
	return p, nil
}

// decorateSystem is decoratePlace for the cavities of a system.
func (s *Service) decorateSystem(ctx context.Context, sys System, err error) (System, error) {
	if err != nil {
		return System{}, err
	}
	cavities := make([]*ListItem, len(sys.Places))
	for i := range sys.Places {
		cavities[i] = &sys.Places[i].ListItem
	}
	if err := s.countActivities(ctx, cavities...); err != nil {
		return System{}, err
	}
	return sys, nil
}
