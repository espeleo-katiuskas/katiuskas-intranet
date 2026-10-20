package users

import (
	"context"
	"time"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/licenses"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/memberships"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/roles"
)

type Service struct {
	repo        *Repository
	roles       *roles.Service
	memberships *memberships.Service
	licenses    *licenses.Service
}

func NewService(repo *Repository, rolesService *roles.Service, membershipsService *memberships.Service, licensesService *licenses.Service) *Service {
	return &Service{repo: repo, roles: rolesService, memberships: membershipsService, licenses: licensesService}
}

// Create inserts the person, then assigns any explicitly requested roles
// as a separate step (not part of the same database transaction as the
// insert — a club-scale app doesn't need cross-package transactions for
// this, but it does mean a failure there leaves a person record behind
// that didn't get its roles). There's no "grant access" step: anyone in
// the table can request a magic link once they have an email and phone
// on file, so a brand-new person just starts with no roles — including
// no "member", which is automatic and derived from an active membership
// period a brand-new person doesn't have yet.
func (s *Service) Create(ctx context.Context, in CreateInput) (User, error) {
	user, err := s.repo.Create(ctx, in)
	if err != nil {
		return user, err
	}
	if err := s.roles.AssignByName(ctx, user.ID, in.RoleNames); err != nil {
		return user, err
	}
	return s.enrich(ctx, user)
}

// EnsureMembershipNumber satisfies memberships.MembershipNumberAssigner —
// memberships.Service.Create calls this after starting a membership
// period, and the repository skips it entirely if the person already has
// a number.
func (s *Service) EnsureMembershipNumber(ctx context.Context, userID int64) error {
	return s.repo.EnsureMembershipNumber(ctx, userID)
}

func (s *Service) Get(ctx context.Context, id int64) (User, error) {
	user, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return user, err
	}
	return s.enrich(ctx, user)
}

// List returns every user on file, ordered by name — deliberately not
// paginated: membership grows by roughly 15 people a year, nowhere near
// the scale where returning everyone in one response is a real cost.
// onlySocios restricts it, at the database level, to people who are or
// have been a socio (see Handler.list, the only caller that ever sets
// it: a plain member's own restricted view) — filtering there rather
// than in Go after the fact so a non-socio row is never fetched from
// Postgres at all, not just hidden from the response.
func (s *Service) List(ctx context.Context, onlySocios bool) ([]User, error) {
	list, err := s.repo.List(ctx, onlySocios)
	if err != nil {
		return nil, err
	}
	return s.enrichMany(ctx, list)
}

func (s *Service) Update(ctx context.Context, id int64, in UpdateInput) (User, error) {
	user, err := s.repo.Update(ctx, id, in)
	if err != nil {
		return user, err
	}
	return s.enrich(ctx, user)
}

// enrich overlays data this package doesn't own: the authoritative role
// list (roles.Service is the single place that knows how to compute
// "member"/"former_member"/"board" from membership/board history on top
// of whatever's actually stored in user_roles — see
// roles.Repository.ListNamesForUser) and the person's "alta/baja" dates.
// Someone who has left and rejoined the club has more than one
// membership period; their alta shown here is the *first* one they ever
// started, not the latest rejoin, while baja (or "still active")
// reflects the latest period — the two dates can come from different
// rows.
func (s *Service) enrich(ctx context.Context, user User) (User, error) {
	enriched, err := s.enrichMany(ctx, []User{user})
	if err != nil {
		return user, err
	}
	return enriched[0], nil
}

// enrichMany is enrich for a whole list: the same overlay, but asking each
// package once for everybody instead of once per person — a listing of the
// whole club (GET /users) used to cost five queries a head, a thousand and a
// half for three hundred people, which was most of what a visit to /socios
// waited for. A single person goes through the same code, so what a listing
// says about somebody can't differ from what their own page says.
func (s *Service) enrichMany(ctx context.Context, users []User) ([]User, error) {
	if len(users) == 0 {
		return users, nil
	}
	ids := make([]int64, len(users))
	for i, u := range users {
		ids[i] = u.ID
	}

	names, err := s.roles.ListNamesForUsers(ctx, ids)
	if err != nil {
		return users, err
	}
	// Newest first (see memberships.Repository.ListByUsers), so periods[0] is
	// the current or most recent period and periods[len-1] the first.
	periods, err := s.memberships.ListByUsers(ctx, ids)
	if err != nil {
		return users, err
	}
	onLeave, err := s.memberships.OnLeaveAmong(ctx, ids)
	if err != nil {
		return users, err
	}
	licenses, err := s.licenses.CurrentByUsers(ctx, ids)
	if err != nil {
		return users, err
	}
	dues, err := s.memberships.LatestDues(ctx, ids)
	if err != nil {
		return users, err
	}

	for i := range users {
		user := &users[i]
		user.Roles = names[user.ID]
		if list := periods[user.ID]; len(list) > 0 {
			latest := list[0]
			first := list[len(list)-1]
			user.MembershipStartDate = &first.StartDate
			user.MembershipEndDate = latest.EndDate
			// A leave doesn't touch the membership period it belongs to, so
			// telling "activo" apart from "baja temporal" needs its own check —
			// only for someone currently active, since a closed period can't
			// have a leave covering today.
			if latest.EndDate == nil {
				user.OnLeave = onLeave[user.ID]
			}
		}
		if license, ok := licenses[user.ID]; ok {
			licenseType, licenseYear, licenseIssued := license.Type, license.Year, license.IssuedDate
			user.LicenseType = &licenseType
			user.LicenseYear = &licenseYear
			user.LicenseIssuedDate = &licenseIssued
		}
		if due := dues[user.ID]; due != nil {
			year := due.Year
			user.LastDueYear = &year
			user.LastDuePaidAt = due.PaidAt
		}
	}
	return users, nil
}

// IdentityForUserID says who somebody is — login and name — with a single light
// query, for GET /auth/me, which every page calls on load and which must not
// cost more than it has to.
func (s *Service) IdentityForUserID(ctx context.Context, id int64) (Identity, error) {
	return s.repo.FindIdentityForID(ctx, id)
}

func (s *Service) Directory(ctx context.Context) ([]DirectoryEntry, error) {
	return s.repo.ListDirectory(ctx)
}

// UserIDForLogin resolves a login to the numeric id it names. Every
// package whose own HTTP boundary accepts a person by login (a route
// like /users/{login}/tokens, or a request body naming someone else)
// needs this translation before it can act against anything keyed by
// user id — those packages can't import this one directly (this one
// already imports several of them), so each defines its own narrow
// interface that *Service satisfies structurally, and main.go wires
// this service in to satisfy it.
func (s *Service) UserIDForLogin(ctx context.Context, login string) (int64, error) {
	return s.repo.FindIDForLogin(ctx, login)
}

// LoginForUserID is UserIDForLogin's inverse, for the few responses that
// need to name a person by an id resolved some other way (see
// authapi.Handler.impersonatorSummary) rather than one a repository JOIN
// already turned into a login inline.
func (s *Service) LoginForUserID(ctx context.Context, id int64) (string, error) {
	return s.repo.FindLoginForID(ctx, id)
}

// Delete refuses to remove the only remaining admin, for the same reason
// roles.Service.Remove refuses to strip just their admin role: someone has
// to be left who can manage users and roles.
func (s *Service) Delete(ctx context.Context, id int64) error {
	isLastAdmin, err := s.roles.IsLastAdmin(ctx, id)
	if err != nil {
		return err
	}
	if isLastAdmin {
		return roles.ErrLastAdmin
	}
	return s.repo.Delete(ctx, id)
}

func (s *Service) FindActiveByEmailAndPhone(ctx context.Context, email, phone string) (User, error) {
	return s.repo.FindActiveByEmailAndPhone(ctx, email, phone)
}

func (s *Service) FindByPhoneNumber(ctx context.Context, digits string) (User, error) {
	return s.repo.FindByPhoneNumber(ctx, digits)
}

func (s *Service) SetTelegramChatID(ctx context.Context, userID, chatID int64) error {
	return s.repo.SetTelegramChatID(ctx, userID, chatID)
}

func (s *Service) ClearTelegramChatID(ctx context.Context, userID int64) error {
	return s.repo.ClearTelegramChatID(ctx, userID)
}

func (s *Service) TelegramChatIDForUser(ctx context.Context, userID int64) (*int64, error) {
	return s.repo.TelegramChatIDForUser(ctx, userID)
}

func (s *Service) UserIDForTelegramChatID(ctx context.Context, chatID int64) (int64, error) {
	return s.repo.UserIDForTelegramChatID(ctx, chatID)
}

func (s *Service) TouchLastLogin(ctx context.Context, userID int64, ip string) error {
	return s.repo.TouchLastLogin(ctx, userID, ip)
}

func (s *Service) CreateMagicLinkToken(ctx context.Context, userID int64, tokenHash, codeHash string, expiresAt time.Time) error {
	return s.repo.CreateMagicLinkToken(ctx, userID, tokenHash, codeHash, expiresAt)
}

func (s *Service) ConsumeMagicLinkToken(ctx context.Context, tokenHash string) (int64, error) {
	return s.repo.ConsumeMagicLinkToken(ctx, tokenHash)
}

func (s *Service) ConsumeMagicLinkCode(ctx context.Context, userID int64, codeHash string) (int64, error) {
	return s.repo.ConsumeMagicLinkCode(ctx, userID, codeHash)
}

func (s *Service) HasRecentMagicLinkToken(ctx context.Context, userID int64, since time.Time) (bool, error) {
	return s.repo.HasRecentMagicLinkToken(ctx, userID, since)
}
