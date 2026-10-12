package licenses

import (
	"context"
	"errors"
	"time"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/memberships"
)

var (
	// ErrNotActiveMember means the target person didn't have an open
	// membership period on the license's own issued_date — someone can
	// still be assigned a license from while they were a socio even
	// after having since caused baja, as long as they actually were one
	// on that date.
	ErrNotActiveMember = errors.New("the person must have had an active membership on the license's issued date")
	// ErrOtherDetailRequired means the chosen license type is the
	// catalog's "Otra" escape hatch, which must say which license it is.
	ErrOtherDetailRequired = errors.New(`other_detail is required when the license type is "Otra"`)
	// ErrOtherDetailNotAllowed means other_detail was given for a
	// license type other than "Otra", where it doesn't apply.
	ErrOtherDetailNotAllowed = errors.New(`other_detail is only allowed when the license type is "Otra"`)
)

type Service struct {
	repo        *Repository
	memberships *memberships.Service
}

func NewService(repo *Repository, membershipsService *memberships.Service) *Service {
	return &Service{repo: repo, memberships: membershipsService}
}

func (s *Service) ListTypes(ctx context.Context) ([]LicenseType, error) {
	return s.repo.ListTypes(ctx)
}

// normalizeOtherDetail applies the "Otra" rule shared by Create and
// Update: the catalog's escape-hatch entry requires a detail string,
// every other entry forbids one. Returns the OtherDetail to actually
// persist (always nil for a non-"Otra" type, even if one was given).
func normalizeOtherDetail(licenseType LicenseType, otherDetail *string) (*string, error) {
	hasDetail := otherDetail != nil && *otherDetail != ""
	switch {
	case licenseType.IsOther && !hasDetail:
		return nil, ErrOtherDetailRequired
	case !licenseType.IsOther && hasDetail:
		return nil, ErrOtherDetailNotAllowed
	case !licenseType.IsOther:
		return nil, nil
	default:
		return otherDetail, nil
	}
}

func (s *Service) Create(ctx context.Context, in CreateInput) (License, error) {
	wasMember, err := s.memberships.WasMemberOn(ctx, in.UserID, in.IssuedDate)
	if err != nil {
		return License{}, err
	}
	if !wasMember {
		return License{}, ErrNotActiveMember
	}

	licenseType, err := s.repo.GetTypeByID(ctx, in.LicenseTypeID)
	if err != nil {
		return License{}, err
	}
	otherDetail, err := normalizeOtherDetail(licenseType, in.OtherDetail)
	if err != nil {
		return License{}, err
	}
	in.OtherDetail = otherDetail

	// Last, after every check that only needs the request itself: the
	// transaction has to exist and have an entry on an account flagged
	// allows_licenses.
	exists, allowed, err := s.repo.TransactionAllowsLicenses(ctx, in.TransactionID)
	if err != nil {
		return License{}, err
	}
	if !exists {
		return License{}, ErrUnknownTransaction
	}
	if !allowed {
		return License{}, ErrTransactionNotForLicenses
	}

	return s.repo.Create(ctx, in)
}

// Update corrects a license's type, other_detail, or issued_date. It
// doesn't re-check the person's membership status the way Create does
// — that gate is for assigning a new license, not for fixing a typo on
// one someone already has, even after they've since left the club.
func (s *Service) Update(ctx context.Context, id int64, in UpdateInput) (License, error) {
	licenseType, err := s.repo.GetTypeByID(ctx, in.LicenseTypeID)
	if err != nil {
		return License{}, err
	}
	otherDetail, err := normalizeOtherDetail(licenseType, in.OtherDetail)
	if err != nil {
		return License{}, err
	}
	in.OtherDetail = otherDetail

	return s.repo.Update(ctx, id, in)
}

// Get returns a single license by id — used by Handler's own mutation
// notifications (a before/after diff on Update, naming what's about to
// be removed on Delete), not by any route of its own.
func (s *Service) Get(ctx context.Context, id int64) (License, error) {
	return s.repo.GetByID(ctx, id)
}

func (s *Service) Delete(ctx context.Context, id int64) error {
	return s.repo.Delete(ctx, id)
}

func (s *Service) ListByUser(ctx context.Context, userID int64) ([]License, error) {
	return s.repo.ListByUser(ctx, userID)
}

// ListByTransaction returns every license recorded against the
// transaction. An unknown transaction id just comes back an empty list,
// the same as one that paid for no licenses.
func (s *Service) ListByTransaction(ctx context.Context, transactionID int64) ([]License, error) {
	return s.repo.ListByTransaction(ctx, transactionID)
}

// LatestByUser returns the person's most recently issued license, or nil
// if they've never had one — used to show a "current" license on the
// people list without a separate per-person round trip for full history.
func (s *Service) LatestByUser(ctx context.Context, userID int64) (*License, error) {
	list, err := s.repo.ListByUser(ctx, userID)
	if err != nil || len(list) == 0 {
		return nil, err
	}
	return &list[0], nil
}

// IsCurrentlyFederated reports whether the person's most recent license
// is still valid — comparing Year directly against the current one
// (not ValidUntil's exact instant), so someone doesn't read as expired
// for the rest of December 31st itself. Used to gate joining a caving
// or canyoning activity to "socios federados".
func (s *Service) IsCurrentlyFederated(ctx context.Context, userID int64) (bool, error) {
	latest, err := s.LatestByUser(ctx, userID)
	if err != nil || latest == nil {
		return false, err
	}
	return latest.Year == time.Now().Year(), nil
}
