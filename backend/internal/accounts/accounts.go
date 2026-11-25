// Package accounts is who acts on the catalogue of places: its own accounts, which have nothing to do
// with the intranet's table of people. An account is a person (what is shown as the author of a change)
// or a service, a client of the catalogue, of which the intranet of the club is the first. A service is
// registered as an issuer: it vouches for the people it sends (the intranet says "this is the person
// whose subject is X, called Y"), and the account of a person is made the first time they act, linked
// to the identity the issuer knows them by. What the catalogue keeps of who did something is the id of
// an account, a foreign key with a meaning inside the catalogue.
package accounts

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/access"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/platform/db"
)

var (
	// ErrUnknownIssuer means the viewer was vouched for by a service the catalogue does not know.
	ErrUnknownIssuer = errors.New("the issuer of the viewer is not a client of the catalogue")
	// ErrNotSignedIn means a viewer with no subject or no name, who cannot have an account.
	ErrNotSignedIn = errors.New("only a viewer with a subject and a name has an account")
)

// Account is whoever acts on the catalogue.
type Account struct {
	ID int64
	// Kind is `person` or `service`.
	Kind string
	// DisplayName is how the account is shown, as the author of a change: for a person, the name the issuer
	// gave the last time they acted.
	DisplayName string
}

type Repository struct {
	pool db.PGX
}

func NewRepository(pool db.PGX) *Repository { return &Repository{pool: pool} }

// Service finds or makes the account of a viewer.
type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service { return &Service{repo: repo} }

// Ensure is the account of the viewer, made if it is the first time they act, with the name the issuer
// gives them now (a name that changed is updated). The issuer has to be a client of the catalogue.
func (s *Service) Ensure(ctx context.Context, viewer access.Viewer) (Account, error) {
	name := strings.TrimSpace(viewer.Name)
	if viewer.Subject == "" || name == "" {
		return Account{}, ErrNotSignedIn
	}
	return s.repo.ensure(ctx, viewer.Issuer, viewer.Subject, name)
}

// Of is the account that the issuer knows by that subject, and whether there is one: a person who has
// never acted has none.
func (s *Service) Of(ctx context.Context, issuer, subject string) (Account, bool, error) {
	var a Account
	err := s.repo.pool.QueryRow(ctx, `
		SELECT a.id, a.kind, a.display_name FROM catalogue.account_identities i
		JOIN catalogue.accounts a ON a.id = i.account_id WHERE i.issuer = $1 AND i.subject = $2`,
		issuer, subject).Scan(&a.ID, &a.Kind, &a.DisplayName)
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, false, nil
	}
	return a, err == nil, err
}

func (r *Repository) ensure(ctx context.Context, issuer, subject, name string) (Account, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Account{}, err
	}
	defer tx.Rollback(ctx)
	var known bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM catalogue.issuers WHERE issuer = $1)`, issuer).Scan(&known); err != nil {
		return Account{}, err
	}
	if !known {
		return Account{}, ErrUnknownIssuer
	}
	// Two requests of a person who acts for the first time at once make one account: the second waits for
	// the lock of the identity (two identities may share a lock, which only makes them wait).
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, issuer+"|"+subject); err != nil {
		return Account{}, err
	}
	var a Account
	err = tx.QueryRow(ctx, `
		SELECT a.id, a.kind, a.display_name FROM catalogue.account_identities i
		JOIN catalogue.accounts a ON a.id = i.account_id WHERE i.issuer = $1 AND i.subject = $2`,
		issuer, subject).Scan(&a.ID, &a.Kind, &a.DisplayName)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		if err := tx.QueryRow(ctx, `INSERT INTO catalogue.accounts (kind, display_name) VALUES ('person', $1) RETURNING id, kind, display_name`,
			name).Scan(&a.ID, &a.Kind, &a.DisplayName); err != nil {
			return Account{}, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO catalogue.account_identities (issuer, subject, account_id) VALUES ($1, $2, $3)`,
			issuer, subject, a.ID); err != nil {
			return Account{}, err
		}
	case err != nil:
		return Account{}, err
	case a.DisplayName != name:
		if _, err := tx.Exec(ctx, `UPDATE catalogue.accounts SET display_name = $2 WHERE id = $1`, a.ID, name); err != nil {
			return Account{}, err
		}
		a.DisplayName = name
	}
	return a, tx.Commit(ctx)
}
