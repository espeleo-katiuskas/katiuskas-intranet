package distributions

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/platform/db"
)

var (
	ErrNotFound     = errors.New("distribution not found")
	ErrUnitNotFound = errors.New("unit not found")
	// ErrHasUnits means somebody has already asked for something, so the
	// distribution can't be deleted and its options can't be replaced.
	ErrHasUnits = errors.New("somebody has already asked for this distribution")
	// ErrHasSettledUnits means some unit was already paid or collected, which
	// stops the distribution from being deleted until those marks are undone.
	ErrHasSettledUnits = errors.New("some units are already paid or collected: undo those marks first")
	// ErrAlreadyRegistered means the beneficiary already has their own
	// (non-extra) unit.
	ErrAlreadyRegistered = errors.New("this person already has their own unit")
	// ErrExtrasNotAllowed means the distribution has no extra_price.
	ErrExtrasNotAllowed = errors.New("this distribution has no extra units")
	// ErrTooManyExtras means the beneficiary already has max_extra extras.
	ErrTooManyExtras = errors.New("this person already has the maximum number of extra units")
	// ErrAlreadyPickedUp / ErrAlreadyPaid mean a unit in the batch already
	// carries that mark.
	ErrAlreadyPickedUp = errors.New("a unit was already collected")
	ErrAlreadyPaid     = errors.New("a unit was already marked as paid")
	// ErrUnknownArticle means the article isn't one of the distribution's.
	ErrUnknownArticle = errors.New("unknown article")
	// ErrArticleExhausted means asking for that many would go over the number of
	// units there are of the article in all.
	ErrArticleExhausted = errors.New("there are not enough units left of this article")
	// ErrCapBelowRequested means an article's limit can't go below what has
	// already been asked for.
	ErrCapBelowRequested = errors.New("an article's limit can't be lower than what has already been asked for")
	// ErrPersonLimit means asking for that many would go over what one person may
	// have of the article.
	ErrPersonLimit = errors.New("this is more than one person may have of this article")
	// ErrIdentifierTaken means another unit of the article already has that number.
	ErrIdentifierTaken = errors.New("another unit of this article already has that number")
	// ErrBelowPaid means a quantity can't go below what has already been paid.
	ErrBelowPaid = errors.New("the quantity can't be lower than what has already been paid")
	// ErrIdentifierNotAssigned, ErrNotPaid and ErrNotDelivered mean numbers named in
	// a request that the row hasn't got at all, or has but not paid or not
	// delivered (what the operation needs them to be); the message ends with which.
	ErrIdentifierNotAssigned = errors.New("these numbers are not assigned to this order")
	ErrNotPaid               = errors.New("these numbers are not paid")
	ErrNotDelivered          = errors.New("these numbers are not handed out")
	// The accounting side of a payment: ErrUnknownAccount is an account that doesn't
	// exist, ErrAccountNotAsset one that isn't an asset account to pay in,
	// ErrAccountRequired a payment of money with no account said,
	// ErrNoDistributionAccount a distribution with no account of its own while none
	// is the default for distributions, and ErrAccountNotIncome a distribution
	// naming an account that isn't of type income.
	ErrUnknownAccount   = errors.New("unknown account")
	ErrAccountNotAsset  = errors.New("the account paid in has to be of type asset")
	ErrAccountNotIncome = errors.New("the account of a distribution has to be of type income")
	// ErrAccountHasChildren is an account that groups others, which can't have
	// transactions: only an account with no child accounts can be paid in or be the
	// account of a distribution.
	ErrAccountHasChildren    = errors.New("an account with child accounts can't have transactions: use one of its children")
	ErrAccountRequired       = errors.New("say the account the payment was made in")
	ErrNoDistributionAccount = errors.New("this distribution has no account for its payments, and none is marked as the default for distributions")
	// ErrUnitPaid means a unit with something paid can't be deleted: the payment
	// has to be taken off first, which records it in the accounting.
	ErrUnitPaid = errors.New("this order has payments: take them off first")
	// ErrBelowNumbered means a quantity can't go below the numbers already given to
	// the row: some have to be taken off first.
	ErrBelowNumbered = errors.New("the quantity can't be lower than the numbers already assigned: take some off first")
	// ErrBelowDelivered means a quantity can't go below what has been handed out.
	ErrBelowDelivered = errors.New("the quantity can't be lower than what has already been handed out")
	// ErrUnknownUser means a given user id doesn't exist.
	ErrUnknownUser = errors.New("unknown user")
)

const (
	uniqueViolationCode     = "23505"
	foreignKeyViolationCode = "23503"
)

// isForeignKeyViolation says whether err is Postgres refusing a reference to
// something that doesn't exist.
func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == foreignKeyViolationCode
}

type Repository struct {
	pool db.PGX
}

func NewRepository(pool db.PGX) *Repository {
	return &Repository{pool: pool}
}

const distributionSelect = `
	SELECT d.id, d.name, d.description, d.audience, d.status, d.closes_at, d.cutoff_date,
	       d.price::float8, d.extra_price::float8, d.max_extra, d.created_at, u.login, d.account_id, ac.name,
	       (SELECT COALESCE(sum(x.quantity), 0) FROM distribution_units x WHERE x.distribution_id = d.id),
	       (SELECT COALESCE(sum(x.picked_up_quantity), 0) FROM distribution_units x WHERE x.distribution_id = d.id)
	FROM distributions d
	JOIN users u ON u.id = d.created_by
	LEFT JOIN accounts ac ON ac.id = d.account_id`

func scanDistribution(row pgx.Row, d *Distribution) error {
	return row.Scan(&d.ID, &d.Name, &d.Description, &d.Audience, &d.Status, &d.ClosesAt, &d.CutoffDate,
		&d.Price, &d.ExtraPrice, &d.MaxExtra, &d.CreatedAt, &d.CreatedByLogin, &d.AccountID, &d.AccountName, &d.UnitCount, &d.PickedUpCount)
}

// attachOptions fills in Options and Articles for every given distribution, in
// two queries.
func (r *Repository) attachOptions(ctx context.Context, list []Distribution) error {
	if len(list) == 0 {
		return nil
	}
	ids := make([]int64, len(list))
	index := make(map[int64]int, len(list))
	for i := range list {
		ids[i] = list[i].ID
		index[list[i].ID] = i
		list[i].Options = []Option{}
	}
	rows, err := r.pool.Query(ctx, `
		SELECT distribution_id, name, "values" FROM distribution_options
		WHERE distribution_id = ANY($1) ORDER BY distribution_id, position
	`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var o Option
		if err := rows.Scan(&id, &o.Name, &o.Values); err != nil {
			return err
		}
		list[index[id]].Options = append(list[index[id]].Options, o)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range list {
		list[i].Articles = []Article{}
	}
	articles, err := r.pool.Query(ctx, `
		SELECT a.distribution_id, a.id, a.name, a.price::float8, a.max_total, a.max_per_person, a.has_identifiers,
		       a.identifier_fields,
		       (SELECT COALESCE(sum(n.quantity), 0) FROM distribution_units n WHERE n.article_id = a.id),
		       (SELECT count(*) FROM distribution_unit_identifiers i WHERE i.article_id = a.id)
		FROM distribution_articles a
		WHERE a.distribution_id = ANY($1) ORDER BY a.distribution_id, a.position
	`, ids)
	if err != nil {
		return err
	}
	defer articles.Close()
	for articles.Next() {
		var id int64
		var a Article
		if err := articles.Scan(&id, &a.ID, &a.Name, &a.Price, &a.MaxTotal, &a.MaxPerPerson, &a.HasIdentifiers,
			&a.IdentifierFields, &a.Requested, &a.IdentifiersAssigned); err != nil {
			return err
		}
		if n, err := buildNumbering(a.IdentifierFields); err == nil && n != nil {
			a.IdentifierTotal = n.total()
		}
		list[index[id]].Articles = append(list[index[id]].Articles, a)
	}
	return articles.Err()
}

// fieldsOrEmpty is fields, as an empty list rather than nil (the column is
// never null).
func fieldsOrEmpty(fields []IdentifierField) []IdentifierField {
	if fields == nil {
		return []IdentifierField{}
	}
	return fields
}

func insertArticles(ctx context.Context, tx pgx.Tx, distributionID int64, articles []ArticleInput) error {
	for i, a := range articles {
		if _, err := tx.Exec(ctx, `
			INSERT INTO distribution_articles (distribution_id, name, price, max_total, max_per_person, has_identifiers, identifier_fields, position)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		`, distributionID, a.Name, a.Price, a.MaxTotal, a.MaxPerPerson, a.HasIdentifiers, fieldsOrEmpty(a.IdentifierFields), i+1); err != nil {
			return err
		}
	}
	return nil
}

func insertOptions(ctx context.Context, tx pgx.Tx, distributionID int64, options []OptionInput) error {
	for i, o := range options {
		if _, err := tx.Exec(ctx, `
			INSERT INTO distribution_options (distribution_id, name, "values", position)
			VALUES ($1, $2, $3, $4)
		`, distributionID, o.Name, o.Values, i+1); err != nil {
			return err
		}
	}
	return nil
}

// checkIncomeAccount says whether the account a distribution names (nil: the
// default one, which is always an income account) exists and is of type income.
func checkIncomeAccount(ctx context.Context, tx pgx.Tx, accountID *int64) error {
	if accountID == nil {
		return nil
	}
	var kind *string
	err := tx.QueryRow(ctx, `SELECT account_effective_type(id)::text FROM accounts WHERE id = $1`, *accountID).Scan(&kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrUnknownAccount
	}
	if err != nil {
		return err
	}
	if kind == nil || *kind != "income" {
		return ErrAccountNotIncome
	}
	return noChildren(ctx, tx, *accountID)
}

// noChildren says ErrAccountHasChildren when the account groups other accounts.
func noChildren(ctx context.Context, tx pgx.Tx, accountID int64) error {
	var has bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM accounts WHERE parent_id = $1)`, accountID).Scan(&has); err != nil {
		return err
	}
	if has {
		return ErrAccountHasChildren
	}
	return nil
}

func (r *Repository) Create(ctx context.Context, in CreateInput) (Distribution, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Distribution{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if err := checkIncomeAccount(ctx, tx, in.AccountID); err != nil {
		return Distribution{}, err
	}
	var id int64
	err = tx.QueryRow(ctx, `
		INSERT INTO distributions (name, description, audience, closes_at, cutoff_date, price, extra_price, max_extra, created_by, account_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING id
	`, in.Name, in.Description, in.Audience, in.ClosesAt, in.CutoffDate, in.Price, in.ExtraPrice, in.MaxExtra, in.CreatedBy, in.AccountID).Scan(&id)
	if isForeignKeyViolation(err) {
		return Distribution{}, ErrUnknownAccount
	}
	if err != nil {
		return Distribution{}, err
	}
	if err := insertOptions(ctx, tx, id, in.Options); err != nil {
		return Distribution{}, err
	}
	if err := insertArticles(ctx, tx, id, in.Articles); err != nil {
		return Distribution{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Distribution{}, err
	}
	return r.Get(ctx, id)
}

func (r *Repository) Get(ctx context.Context, id int64) (Distribution, error) {
	var d Distribution
	err := scanDistribution(r.pool.QueryRow(ctx, distributionSelect+` WHERE d.id = $1`, id), &d)
	if errors.Is(err, pgx.ErrNoRows) {
		return Distribution{}, ErrNotFound
	}
	if err != nil {
		return Distribution{}, err
	}
	list := []Distribution{d}
	if err := r.attachOptions(ctx, list); err != nil {
		return Distribution{}, err
	}
	return list[0], nil
}

// List returns the newest first; includeDrafts is only for whoever manages
// them.
func (r *Repository) List(ctx context.Context, includeDrafts bool) ([]Distribution, error) {
	rows, err := r.pool.Query(ctx, distributionSelect+`
		WHERE $1 OR d.status <> 'draft'
		ORDER BY d.created_at DESC, d.id DESC
	`, includeDrafts)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []Distribution{}
	for rows.Next() {
		var d Distribution
		if err := scanDistribution(rows, &d); err != nil {
			return nil, err
		}
		list = append(list, d)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := r.attachOptions(ctx, list); err != nil {
		return nil, err
	}
	return list, nil
}

// Update applies in; a nil Options or Status is left alone. Replacing the
// options fails with ErrHasUnits once any unit exists.
func (r *Repository) Update(ctx context.Context, id int64, in UpdateInput) (Distribution, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Distribution{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var previous Status
	var previousAudience Audience
	err = tx.QueryRow(ctx, `SELECT status, audience FROM distributions WHERE id = $1 FOR UPDATE`, id).Scan(&previous, &previousAudience)
	if errors.Is(err, pgx.ErrNoRows) {
		return Distribution{}, ErrNotFound
	}
	if err != nil {
		return Distribution{}, err
	}

	// Going back to a draft starts it over: every unit goes — everybody's own
	// (made automatically, and made again when it opens), the extras and, in one
	// people sign up for, the sign-ups — with whatever was chosen on them, which
	// may not fit the options it is about to be given. Paid or collected ones are
	// real things that happened, so those stop it until the marks are undone.
	backToDraft := in.Status != nil && *in.Status == StatusDraft && previous != StatusDraft
	// Turning a distribution for all members into one people sign up for takes
	// the automatic units with it — the ones nobody has touched, which exist only
	// because everybody was in. What somebody did choose stays: that is somebody
	// who asked for it, which is what signing up is. Same refusal for a paid or
	// collected one.
	becomesSignup := previousAudience == AudienceAllMembers && in.Audience == AudienceSignup
	if backToDraft || becomesSignup {
		// Everything on a draft that goes away, or only what nobody touched.
		scope := `TRUE`
		if !backToDraft {
			scope = `NOT is_extra AND registered_by_id IS NULL`
		}
		var settled int
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM distribution_units
			WHERE distribution_id = $1 AND `+scope+` AND (paid_at IS NOT NULL OR picked_up_at IS NOT NULL)
		`, id).Scan(&settled); err != nil {
			return Distribution{}, err
		}
		if settled > 0 {
			return Distribution{}, ErrHasSettledUnits
		}
		if _, err := tx.Exec(ctx, `DELETE FROM distribution_units WHERE distribution_id = $1 AND `+scope, id); err != nil {
			return Distribution{}, err
		}
	}

	if err := checkIncomeAccount(ctx, tx, in.AccountID); err != nil {
		return Distribution{}, err
	}
	tag, err := tx.Exec(ctx, `
		UPDATE distributions SET
			name = $2, description = $3, audience = $4, closes_at = $5, cutoff_date = $6,
			price = $7, extra_price = $8, max_extra = $9,
			status = COALESCE($10, status), account_id = $11
		WHERE id = $1
	`, id, in.Name, in.Description, in.Audience, in.ClosesAt, in.CutoffDate, in.Price, in.ExtraPrice, in.MaxExtra, in.Status, in.AccountID)
	if isForeignKeyViolation(err) {
		return Distribution{}, ErrUnknownAccount
	}
	if err != nil {
		return Distribution{}, err
	}
	if tag.RowsAffected() == 0 {
		return Distribution{}, ErrNotFound
	}
	// A price change reaches the automatic units nobody has touched yet — not
	// chosen (nobody entered them), not paid, not collected — which got the old
	// price only because they were made at opening, so correcting the price of a
	// distribution that is already open doesn't leave them on the old one.
	// Everything somebody has already agreed to keeps what it cost.
	if _, err := tx.Exec(ctx, `
		UPDATE distribution_units SET price = $2
		WHERE distribution_id = $1 AND registered_by_id IS NULL AND NOT is_extra AND article_id IS NULL
		  AND paid_at IS NULL AND picked_up_at IS NULL AND price <> $2
	`, id, in.Price); err != nil {
		return Distribution{}, err
	}
	same, err := sameOptions(ctx, tx, id, in.Options)
	if err != nil {
		return Distribution{}, err
	}
	if in.Options != nil && !same {
		var units int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM distribution_units WHERE distribution_id = $1`, id).Scan(&units); err != nil {
			return Distribution{}, err
		}
		if units > 0 {
			return Distribution{}, ErrHasUnits
		}
		if _, err := tx.Exec(ctx, `DELETE FROM distribution_options WHERE distribution_id = $1`, id); err != nil {
			return Distribution{}, err
		}
		if err := insertOptions(ctx, tx, id, *in.Options); err != nil {
			return Distribution{}, err
		}
	}
	if in.Articles != nil {
		if err := replaceArticles(ctx, tx, id, *in.Articles); err != nil {
			return Distribution{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Distribution{}, err
	}
	return r.Get(ctx, id)
}

// replaceArticles makes articles the distribution's, in that order. Sending
// back what is already there changes nothing. Once any unit exists the
// articles themselves are fixed — units point at them — and only each one's
// limits (MaxTotal, MaxPerPerson) and HasIdentifiers may change, the limits to no
// less than what has already been asked for: ErrHasUnits for anything else,
// ErrCapBelowRequested for a limit that is too low.
func replaceArticles(ctx context.Context, tx pgx.Tx, id int64, articles []ArticleInput) error {
	rows, err := tx.Query(ctx, `
		SELECT a.id, a.name, a.price::float8, a.max_total, a.max_per_person, a.has_identifiers, a.identifier_fields,
		       (SELECT COALESCE(sum(n.quantity), 0) FROM distribution_units n WHERE n.article_id = a.id),
		       (SELECT COALESCE(max(n.quantity), 0) FROM distribution_units n WHERE n.article_id = a.id)
		FROM distribution_articles a WHERE a.distribution_id = $1 ORDER BY a.position
	`, id)
	if err != nil {
		return err
	}
	defer rows.Close()
	var current []Article
	var biggest []int
	for rows.Next() {
		var a Article
		var most int
		if err := rows.Scan(&a.ID, &a.Name, &a.Price, &a.MaxTotal, &a.MaxPerPerson, &a.HasIdentifiers, &a.IdentifierFields, &a.Requested, &most); err != nil {
			return err
		}
		current = append(current, a)
		biggest = append(biggest, most)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()

	var units int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM distribution_units WHERE distribution_id = $1`, id).Scan(&units); err != nil {
		return err
	}
	if units == 0 {
		same := slices.EqualFunc(current, articles, func(a Article, b ArticleInput) bool {
			return a.Name == b.Name && a.Price == b.Price && intPtrsEqual(a.MaxTotal, b.MaxTotal) &&
				intPtrsEqual(a.MaxPerPerson, b.MaxPerPerson) && a.HasIdentifiers == b.HasIdentifiers &&
				slices.Equal(a.IdentifierFields, fieldsOrEmpty(b.IdentifierFields))
		})
		if same {
			return nil
		}
		if _, err := tx.Exec(ctx, `DELETE FROM distribution_articles WHERE distribution_id = $1`, id); err != nil {
			return err
		}
		return insertArticles(ctx, tx, id, articles)
	}
	if len(current) != len(articles) {
		return ErrHasUnits
	}
	for i, a := range articles {
		if current[i].Name != a.Name || current[i].Price != a.Price {
			return ErrHasUnits
		}
		if (a.MaxTotal != nil && *a.MaxTotal < current[i].Requested) ||
			(a.MaxPerPerson != nil && *a.MaxPerPerson < biggest[i]) {
			return ErrCapBelowRequested
		}
	}
	for i, a := range articles {
		// New numbers can be added (another series bought) but not taken away from
		// somebody who has them.
		if n, err := buildNumbering(a.IdentifierFields); err != nil {
			return err
		} else if n != nil {
			assigned, err := articleIdentifiers(ctx, tx, current[i].ID)
			if err != nil {
				return err
			}
			for _, identifier := range assigned {
				if !n.valid(identifier) {
					return ErrNumberInUse
				}
			}
		}
		if _, err := tx.Exec(ctx, `
			UPDATE distribution_articles SET max_total = $2, max_per_person = $3, has_identifiers = $4, identifier_fields = $5 WHERE id = $1
		`, current[i].ID, a.MaxTotal, a.MaxPerPerson, a.HasIdentifiers, fieldsOrEmpty(a.IdentifierFields)); err != nil {
			return err
		}
	}
	return nil
}

func intPtrsEqual(a, b *int) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// sameOptions reports whether options is nil or exactly what the distribution
// already has, so a form that sends everything back doesn't count as changing
// them.
func sameOptions(ctx context.Context, tx pgx.Tx, id int64, options *[]OptionInput) (bool, error) {
	if options == nil {
		return true, nil
	}
	rows, err := tx.Query(ctx, `SELECT name, "values" FROM distribution_options WHERE distribution_id = $1 ORDER BY position`, id)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	var current []OptionInput
	for rows.Next() {
		var o OptionInput
		if err := rows.Scan(&o.Name, &o.Values); err != nil {
			return false, err
		}
		current = append(current, o)
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	return slices.EqualFunc(current, *options, func(a, b OptionInput) bool {
		return a.Name == b.Name && slices.Equal(a.Values, b.Values)
	}), nil
}

// Delete removes a distribution together with its units (what members chose
// goes with it), unless some unit was already paid or collected — those marks
// are real things that happened, and have to be undone first, deliberately.
func (r *Repository) Delete(ctx context.Context, id int64) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var settled int
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM distribution_units
		WHERE distribution_id = $1 AND (paid_at IS NOT NULL OR picked_up_at IS NOT NULL)
	`, id).Scan(&settled); err != nil {
		return err
	}
	if settled > 0 {
		return ErrHasSettledUnits
	}
	tag, err := tx.Exec(ctx, `DELETE FROM distributions WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return tx.Commit(ctx)
}

// WasMemberOn reports whether userID had a membership period covering the day.
func (r *Repository) WasMemberOn(ctx context.Context, userID int64, day time.Time) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM memberships WHERE user_id = $1 AND period @> $2::date)
	`, userID, day).Scan(&exists)
	return exists, err
}

// WasMemberNowOrOn reports whether userID is a member today or, when day is
// given, was one on that day.
func (r *Repository) WasMemberNowOrOn(ctx context.Context, userID int64, day *time.Time) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM memberships
			WHERE user_id = $1 AND (period @> CURRENT_DATE OR ($2::date IS NOT NULL AND period @> $2::date))
		)
	`, userID, day).Scan(&exists)
	return exists, err
}

// ListMembers returns the people who are members today (when today is set)
// and/or were members on the given day, by name.
func (r *Repository) ListMembers(ctx context.Context, today bool, day *time.Time) ([]Person, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT u.login, u.first_name, u.last_name
		FROM users u
		WHERE EXISTS (
			SELECT 1 FROM memberships m
			WHERE m.user_id = u.id AND (($1 AND m.period @> CURRENT_DATE) OR ($2::date IS NOT NULL AND m.period @> $2::date))
		)
		ORDER BY u.first_name, u.last_name, u.id
	`, today, day)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []Person{}
	for rows.Next() {
		var p Person
		if err := rows.Scan(&p.Login, &p.FirstName, &p.LastName); err != nil {
			return nil, err
		}
		list = append(list, p)
	}
	return list, rows.Err()
}

const unitSelect = `
	SELECT n.id, n.distribution_id, n.beneficiary_id, b.login, b.first_name || ' ' || b.last_name,
	       n.registered_by_id, rb.login, n.registered_at, n.is_extra, n.article_id, ar.name, n.quantity,
	       n.choices, n.price::float8,
	       COALESCE((SELECT array_agg(i.identifier ORDER BY i.id) FROM distribution_unit_identifiers i WHERE i.unit_id = n.id), '{}'),
	       COALESCE((SELECT array_agg(i.identifier ORDER BY i.id) FROM distribution_unit_identifiers i WHERE i.unit_id = n.id AND i.delivered_at IS NOT NULL), '{}'),
	       COALESCE((SELECT array_agg(i.identifier ORDER BY i.id) FROM distribution_unit_identifiers i WHERE i.unit_id = n.id AND i.paid_at IS NOT NULL), '{}'),
	       n.paid_quantity, n.paid_at, pm.login, n.picked_up_quantity, n.picked_up_at, pr.login, pu.login
	FROM distribution_units n
	LEFT JOIN distribution_articles ar ON ar.id = n.article_id
	JOIN users b ON b.id = n.beneficiary_id
	LEFT JOIN users rb ON rb.id = n.registered_by_id
	LEFT JOIN users pm ON pm.id = n.paid_marked_by_id
	LEFT JOIN users pr ON pr.id = n.pickup_recorded_by_id
	LEFT JOIN users pu ON pu.id = n.picked_up_by_id`

func scanUnit(row pgx.Row, u *Unit) error {
	return row.Scan(&u.ID, &u.DistributionID, &u.BeneficiaryID, &u.BeneficiaryLogin, &u.BeneficiaryName,
		&u.RegisteredByID, &u.RegisteredByLogin, &u.RegisteredAt, &u.IsExtra, &u.ArticleID, &u.ArticleName,
		&u.Quantity, &u.Choices, &u.Price, &u.Identifiers, &u.DeliveredIdentifiers, &u.PaidIdentifiers,
		&u.PaidQuantity, &u.PaidAt, &u.PaidMarkedByLogin, &u.PickedUpQuantity, &u.PickedUpAt, &u.PickupRecordedByLogin, &u.PickedUpByLogin)
}

// CreateUnit enters one unit, taking its price from the distribution as it is
// right now. The distribution's row is locked while the extras are counted,
// so two people adding extras for the same person at once can't both slip
// under the cap.
func (r *Repository) CreateUnit(ctx context.Context, in RegisterInput) (Unit, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Unit{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var price float64
	var extraPrice *float64
	var maxExtra *int
	err = tx.QueryRow(ctx, `
		SELECT price::float8, extra_price::float8, max_extra FROM distributions WHERE id = $1 FOR UPDATE
	`, in.DistributionID).Scan(&price, &extraPrice, &maxExtra)
	if errors.Is(err, pgx.ErrNoRows) {
		return Unit{}, ErrNotFound
	}
	if err != nil {
		return Unit{}, err
	}
	if in.IsExtra {
		if extraPrice == nil {
			return Unit{}, ErrExtrasNotAllowed
		}
		price = *extraPrice
		if maxExtra != nil {
			var extras int
			if err := tx.QueryRow(ctx, `
				SELECT count(*) FROM distribution_units
				WHERE distribution_id = $1 AND beneficiary_id = $2 AND is_extra
			`, in.DistributionID, in.BeneficiaryID).Scan(&extras); err != nil {
				return Unit{}, err
			}
			if extras >= *maxExtra {
				return Unit{}, ErrTooManyExtras
			}
		}
	}

	// A member's own unit in a distribution for all members already exists. If
	// nobody has chosen its options yet, asking for it is choosing them: it
	// takes this person as who entered it, and the moment.
	if !in.IsExtra {
		var existingID int64
		var existingChoices map[string]string
		var enteredBy *int64
		err := tx.QueryRow(ctx, `
			SELECT id, choices, registered_by_id FROM distribution_units
			WHERE distribution_id = $1 AND beneficiary_id = $2 AND NOT is_extra AND article_id IS NULL
			FOR UPDATE
		`, in.DistributionID, in.BeneficiaryID).Scan(&existingID, &existingChoices, &enteredBy)
		if err == nil && enteredBy == nil && len(existingChoices) == 0 && len(in.Choices) > 0 {
			if _, err := tx.Exec(ctx, `
				UPDATE distribution_units SET choices = $2, registered_by_id = $3, registered_at = now() WHERE id = $1
			`, existingID, in.Choices, in.RegisteredByID); err != nil {
				return Unit{}, err
			}
			if err := tx.Commit(ctx); err != nil {
				return Unit{}, err
			}
			return r.GetUnit(ctx, in.DistributionID, existingID)
		}
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return Unit{}, err
		}
	}

	var id int64
	err = tx.QueryRow(ctx, `
		INSERT INTO distribution_units (distribution_id, beneficiary_id, registered_by_id, is_extra, choices, price)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id
	`, in.DistributionID, in.BeneficiaryID, in.RegisteredByID, in.IsExtra, in.Choices, price).Scan(&id)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case uniqueViolationCode:
			return Unit{}, ErrAlreadyRegistered
		case foreignKeyViolationCode:
			return Unit{}, ErrUnknownUser
		}
	}
	if err != nil {
		return Unit{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Unit{}, err
	}
	return r.GetUnit(ctx, in.DistributionID, id)
}

// checkQuantity says whether quantity units of the article can be had by one
// person: not more than the article's per-person cap, nor than are left of it in
// all (nil limits are no limit), with the article's row (and so the
// distribution's, which its caller has locked) fixed for the rest of the
// transaction. quantity is what the unit being entered or changed will stand for
// (except, 0 for a new one, is that unit, whose present quantity doesn't count),
// and a person's cap counts every row they have of the article.
func checkQuantity(ctx context.Context, tx pgx.Tx, articleID, except, beneficiaryID int64, quantity int) error {
	var maxTotal, perPerson *int
	var requested, theirs int
	err := tx.QueryRow(ctx, `
		SELECT a.max_total, a.max_per_person,
		       (SELECT COALESCE(sum(n.quantity), 0) FROM distribution_units n WHERE n.article_id = a.id AND n.id <> $2),
		       (SELECT COALESCE(sum(n.quantity), 0) FROM distribution_units n WHERE n.article_id = a.id AND n.id <> $2 AND n.beneficiary_id = $3)
		FROM distribution_articles a WHERE a.id = $1
	`, articleID, except, beneficiaryID).Scan(&maxTotal, &perPerson, &requested, &theirs)
	if err != nil {
		return err
	}
	if perPerson != nil && theirs+quantity > *perPerson {
		return fmt.Errorf("%w: %d at most", ErrPersonLimit, *perPerson)
	}
	if maxTotal != nil && quantity > *maxTotal-requested {
		return fmt.Errorf("%w: %d left", ErrArticleExhausted, max(0, *maxTotal-requested))
	}
	return nil
}

// CreateArticleUnit enters what the beneficiary asks for of one article:
// Quantity units at the article's price as it is now. The distribution's row is
// locked while what is left is counted, so two people asking at once can't
// both take the last ones.
func (r *Repository) CreateArticleUnit(ctx context.Context, in RegisterInput) (Unit, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Unit{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var locked int64
	err = tx.QueryRow(ctx, `SELECT id FROM distributions WHERE id = $1 FOR UPDATE`, in.DistributionID).Scan(&locked)
	if errors.Is(err, pgx.ErrNoRows) {
		return Unit{}, ErrNotFound
	}
	if err != nil {
		return Unit{}, err
	}
	var price float64
	err = tx.QueryRow(ctx, `
		SELECT price::float8 FROM distribution_articles WHERE id = $1 AND distribution_id = $2
	`, *in.ArticleID, in.DistributionID).Scan(&price)
	if errors.Is(err, pgx.ErrNoRows) {
		return Unit{}, ErrUnknownArticle
	}
	if err != nil {
		return Unit{}, err
	}
	// Asking again for something already asked for adds to the person's row of it,
	// if there is one — whatever is paid or handed out in it stays as it was, so
	// what is added is still to pay and to hand out — and otherwise makes one.
	var rowID int64
	var rowQuantity int
	err = tx.QueryRow(ctx, `
		SELECT id, quantity FROM distribution_units
		WHERE article_id = $1 AND beneficiary_id = $2
		ORDER BY id LIMIT 1 FOR UPDATE
	`, *in.ArticleID, in.BeneficiaryID).Scan(&rowID, &rowQuantity)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Unit{}, err
	}
	if err == nil {
		if err := checkQuantity(ctx, tx, *in.ArticleID, rowID, in.BeneficiaryID, rowQuantity+in.Quantity); err != nil {
			return Unit{}, err
		}
		if _, err := tx.Exec(ctx, `UPDATE distribution_units SET quantity = quantity + $2 WHERE id = $1`, rowID, in.Quantity); err != nil {
			return Unit{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return Unit{}, err
		}
		return r.GetUnit(ctx, in.DistributionID, rowID)
	}
	if err := checkQuantity(ctx, tx, *in.ArticleID, 0, in.BeneficiaryID, in.Quantity); err != nil {
		return Unit{}, err
	}

	var id int64
	err = tx.QueryRow(ctx, `
		INSERT INTO distribution_units (distribution_id, beneficiary_id, registered_by_id, article_id, quantity, price)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id
	`, in.DistributionID, in.BeneficiaryID, in.RegisteredByID, *in.ArticleID, in.Quantity, price).Scan(&id)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case foreignKeyViolationCode:
			return Unit{}, ErrUnknownUser
		}
	}
	if err != nil {
		return Unit{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Unit{}, err
	}
	return r.GetUnit(ctx, in.DistributionID, id)
}

// UpdateQuantity changes how many units a row of an article stands for, under
// the same limit as asking for them. Who entered it and when stay as they were.
func (r *Repository) UpdateQuantity(ctx context.Context, distributionID, id int64, quantity int) (Unit, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Unit{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var locked int64
	err = tx.QueryRow(ctx, `SELECT id FROM distributions WHERE id = $1 FOR UPDATE`, distributionID).Scan(&locked)
	if errors.Is(err, pgx.ErrNoRows) {
		return Unit{}, ErrNotFound
	}
	if err != nil {
		return Unit{}, err
	}
	var articleID *int64
	var beneficiaryID int64
	var paid, picked, numbered int
	err = tx.QueryRow(ctx, `
		SELECT article_id, beneficiary_id, paid_quantity, picked_up_quantity,
		       (SELECT count(*) FROM distribution_unit_identifiers i WHERE i.unit_id = distribution_units.id)
		FROM distribution_units WHERE id = $1 AND distribution_id = $2
	`, id, distributionID).Scan(&articleID, &beneficiaryID, &paid, &picked, &numbered)
	if errors.Is(err, pgx.ErrNoRows) {
		return Unit{}, ErrUnitNotFound
	}
	if err != nil {
		return Unit{}, err
	}
	if articleID == nil {
		return Unit{}, ErrInvalidQuantity
	}
	// Not below what is paid, nor below the numbers already written down.
	if quantity < paid {
		return Unit{}, ErrBelowPaid
	}
	if quantity < picked {
		return Unit{}, ErrBelowDelivered
	}
	if quantity < numbered {
		return Unit{}, ErrBelowNumbered
	}
	if err := checkQuantity(ctx, tx, *articleID, id, beneficiaryID, quantity); err != nil {
		return Unit{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE distribution_units SET quantity = $3 WHERE id = $1 AND distribution_id = $2`, id, distributionID, quantity); err != nil {
		return Unit{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Unit{}, err
	}
	return r.GetUnit(ctx, distributionID, id)
}

// SyncAutomaticUnits gives every member on the cutoff date of a distribution
// for all members their own unit, if they have none yet — nobody has to sign
// up. The unit starts with no choices and nobody as who entered it, at the
// price the distribution has now. It does nothing for any other distribution,
// for a draft (nobody sees it yet) or for a finished one, and it is safe to
// run at any time and concurrently: a unit that exists is left alone. A member
// whose unit was already made is not touched, whatever happened to it since.
func (r *Repository) SyncAutomaticUnits(ctx context.Context, d Distribution) error {
	if d.Audience != AudienceAllMembers || d.CutoffDate == nil || d.Status == StatusDraft || d.Status == StatusDone {
		return nil
	}
	_, err := r.pool.Exec(ctx, `
		INSERT INTO distribution_units (distribution_id, beneficiary_id, is_extra, choices, price)
		SELECT $1, m.user_id, false, '{}', $3::numeric
		FROM (SELECT DISTINCT user_id FROM memberships WHERE period @> $2::date) m
		ON CONFLICT (distribution_id, beneficiary_id) WHERE NOT is_extra AND article_id IS NULL DO NOTHING
	`, d.ID, d.CutoffDate, d.Price)
	return err
}

// GetUnit returns a unit of the given distribution.
func (r *Repository) GetUnit(ctx context.Context, distributionID, id int64) (Unit, error) {
	var u Unit
	err := scanUnit(r.pool.QueryRow(ctx, unitSelect+` WHERE n.id = $1 AND n.distribution_id = $2`, id, distributionID), &u)
	if errors.Is(err, pgx.ErrNoRows) {
		return Unit{}, ErrUnitNotFound
	}
	return u, err
}

// ListUnits returns a distribution's units, oldest first; involving limits
// them to those for or entered by that person.
func (r *Repository) ListUnits(ctx context.Context, distributionID int64, involving *int64) ([]Unit, error) {
	rows, err := r.pool.Query(ctx, unitSelect+`
		WHERE n.distribution_id = $1 AND ($2::bigint IS NULL OR n.beneficiary_id = $2 OR n.registered_by_id = $2)
		ORDER BY b.first_name, b.last_name, n.beneficiary_id, n.is_extra, ar.position, n.id
	`, distributionID, involving)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []Unit{}
	for rows.Next() {
		var u Unit
		if err := scanUnit(rows, &u); err != nil {
			return nil, err
		}
		list = append(list, u)
	}
	return list, rows.Err()
}

// UpdateChoices changes what was chosen for a unit. Who entered it and when
// stay as they were, except for an automatic unit nobody had entered yet: the
// first person to choose becomes who did, at that moment.
func (r *Repository) UpdateChoices(ctx context.Context, distributionID, id int64, choices map[string]string, actorID int64) (Unit, error) {
	tag, err := r.pool.Exec(ctx, `
		UPDATE distribution_units SET
			choices = $3,
			registered_at = CASE WHEN registered_by_id IS NULL AND $3::jsonb <> '{}'::jsonb THEN now() ELSE registered_at END,
			registered_by_id = CASE WHEN $3::jsonb <> '{}'::jsonb THEN COALESCE(registered_by_id, $4) ELSE registered_by_id END
		WHERE id = $1 AND distribution_id = $2
	`, id, distributionID, choices, actorID)
	if err != nil {
		return Unit{}, err
	}
	if tag.RowsAffected() == 0 {
		return Unit{}, ErrUnitNotFound
	}
	return r.GetUnit(ctx, distributionID, id)
}

// DeleteUnit removes a unit — one with something paid can't go (ErrUnitPaid): the
// payment has to be taken off first, which records it in the accounting.
func (r *Repository) DeleteUnit(ctx context.Context, distributionID, id int64) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	st, err := lockUnit(ctx, tx, distributionID, id)
	if err != nil {
		return err
	}
	if st.paid > 0 {
		return ErrUnitPaid
	}
	if _, err := tx.Exec(ctx, `DELETE FROM distribution_units WHERE id = $1`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// loggedPayment is what a payment entry says, for the transaction that records it.
type loggedPayment struct {
	id            int64
	beneficiaryID int64
	beneficiary   string // the person's name
	distribution  string // the distribution's name
	description   string
	quantity      int
	amount        float64
	identifiers   []string
}

// logPayment writes down in the list of payments that quantity units of the unit
// were paid (positive) or had their payment taken off (negative), with the
// numbers they were about, and says what it wrote.
func logPayment(ctx context.Context, tx pgx.Tx, unitID int64, quantity int, identifiers []string, byID int64) (loggedPayment, error) {
	if identifiers == nil {
		identifiers = []string{}
	}
	l := loggedPayment{quantity: quantity, identifiers: identifiers}
	var distributionID int64
	err := tx.QueryRow(ctx, `
		INSERT INTO distribution_payments (distribution_id, unit_id, beneficiary_id, description, quantity, amount, identifiers, recorded_by_id)
		SELECT n.distribution_id, n.id, n.beneficiary_id, COALESCE(a.name, d.name), $2::integer, n.price * $2::integer, $3, $4
		FROM distribution_units n
		JOIN distributions d ON d.id = n.distribution_id
		LEFT JOIN distribution_articles a ON a.id = n.article_id
		WHERE n.id = $1
		RETURNING id, distribution_id, beneficiary_id, description, amount::float8
	`, unitID, quantity, identifiers, byID).Scan(&l.id, &distributionID, &l.beneficiaryID, &l.description, &l.amount)
	if err != nil {
		return loggedPayment{}, err
	}
	err = tx.QueryRow(ctx, `
		SELECT u.first_name || ' ' || u.last_name, d.name FROM users u, distributions d WHERE u.id = $1 AND d.id = $2
	`, l.beneficiaryID, distributionID).Scan(&l.beneficiary, &l.distribution)
	return l, err
}

// paymentAccounts checks the account a payment is made in and says which account
// the distribution's payments are recorded in: the asset account has to exist and
// be of type asset, the other is the distribution's own or else the one marked as
// the default for distributions, and they can't be the same.
func paymentAccounts(ctx context.Context, tx pgx.Tx, distributionID, assetID int64) (int64, error) {
	var kind *string
	err := tx.QueryRow(ctx, `SELECT account_effective_type(id)::text FROM accounts WHERE id = $1`, assetID).Scan(&kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrUnknownAccount
	}
	if err != nil {
		return 0, err
	}
	if kind == nil || *kind != "asset" {
		return 0, ErrAccountNotAsset
	}
	if err := noChildren(ctx, tx, assetID); err != nil {
		return 0, err
	}
	var recordedIn *int64
	err = tx.QueryRow(ctx, `
		SELECT COALESCE(d.account_id, (SELECT id FROM accounts WHERE distribution_default))
		FROM distributions d WHERE d.id = $1
	`, distributionID).Scan(&recordedIn)
	if err != nil {
		return 0, err
	}
	if recordedIn == nil {
		return 0, ErrNoDistributionAccount
	}
	return *recordedIn, nil
}

// recordPaymentTransactions records in the accounting what was just paid (or taken
// off), one transaction for each person it was for: money moves between the asset
// account it was paid in and the account of the distribution, with a description
// in Spanish of what was paid and by whom, and the entries of the list of payments
// point at it. A payment taken off is the same with the signs changed. Nothing is
// recorded for what costs nothing; money with no account said is an error.
func recordPaymentTransactions(ctx context.Context, tx pgx.Tx, distributionID, assetID int64, logged []loggedPayment) error {
	var order []int64
	groups := map[int64][]loggedPayment{}
	for _, l := range logged {
		if _, seen := groups[l.beneficiaryID]; !seen {
			order = append(order, l.beneficiaryID)
		}
		groups[l.beneficiaryID] = append(groups[l.beneficiaryID], l)
	}
	var recordedIn int64
	for _, person := range order {
		group := groups[person]
		total := 0.0
		for _, l := range group {
			total += l.amount
		}
		total = math.Round(total*100) / 100
		if total == 0 {
			continue
		}
		if assetID == 0 {
			return ErrAccountRequired
		}
		if recordedIn == 0 {
			var err error
			if recordedIn, err = paymentAccounts(ctx, tx, distributionID, assetID); err != nil {
				return err
			}
		}
		var transactionID int64
		if err := tx.QueryRow(ctx, `
			INSERT INTO transactions (description, occurred_at) VALUES ($1, now()) RETURNING id
		`, paymentDescription(group, total)).Scan(&transactionID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO transaction_entries (transaction_id, account_id, amount) VALUES ($1, $2, $4), ($1, $3, -$4::numeric)
		`, transactionID, assetID, recordedIn, total); err != nil {
			// The account of the distribution (maybe the default one) may group others.
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.ConstraintName == "account_has_children" {
				return ErrAccountHasChildren
			}
			return err
		}
		ids := make([]int64, len(group))
		for i, l := range group {
			ids[i] = l.id
		}
		if _, err := tx.Exec(ctx, `UPDATE distribution_payments SET transaction_id = $2 WHERE id = ANY($1)`, ids, transactionID); err != nil {
			return err
		}
	}
	return nil
}

// paymentDescription says in Spanish what a transaction of payments is: who paid
// what in which distribution, or the payment taken off.
func paymentDescription(group []loggedPayment, total float64) string {
	parts := make([]string, len(group))
	for i, l := range group {
		parts[i] = fmt.Sprintf("%d × %s", max(l.quantity, -l.quantity), l.description)
		if len(l.identifiers) > 0 {
			parts[i] += " (" + formatNumbers(l.identifiers) + ")"
		}
	}
	what := strings.Join(parts, "; ")
	if total < 0 {
		return fmt.Sprintf("Devolución del pago de %s en «%s»: %s", group[0].beneficiary, group[0].distribution, what)
	}
	return fmt.Sprintf("Pago de %s en «%s»: %s", group[0].beneficiary, group[0].distribution, what)
}

// ListPayments lists the payments, newest first, with what each distribution had
// collected after each — summed over its whole history, whatever the filter
// leaves out, so it never restarts at the edge of a range.
func (r *Repository) ListPayments(ctx context.Context, f PaymentFilter) ([]PaymentEntry, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT * FROM (
			SELECT p.id, p.distribution_id, d.name, p.unit_id, p.transaction_id, p.occurred_at,
			       rb.login, rb.first_name || ' ' || rb.last_name, b.login, b.first_name || ' ' || b.last_name,
			       p.description, p.quantity, p.amount::float8, p.identifiers,
			       SUM(p.amount) OVER (PARTITION BY p.distribution_id ORDER BY p.occurred_at, p.id)::float8 AS total_after
			FROM distribution_payments p
			JOIN distributions d ON d.id = p.distribution_id
			JOIN users rb ON rb.id = p.recorded_by_id
			JOIN users b ON b.id = p.beneficiary_id
			WHERE ($1::bigint IS NULL OR p.distribution_id = $1)
		) x
		WHERE ($2::timestamptz IS NULL OR occurred_at >= $2::timestamptz)
		  AND ($3::timestamptz IS NULL OR occurred_at < $3::timestamptz + interval '1 day')
		ORDER BY occurred_at DESC, id DESC
		LIMIT $4 OFFSET $5
	`, f.DistributionID, f.From, f.To, f.Limit, f.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []PaymentEntry{}
	for rows.Next() {
		var e PaymentEntry
		if err := rows.Scan(&e.ID, &e.DistributionID, &e.DistributionName, &e.UnitID, &e.TransactionID, &e.OccurredAt,
			&e.RecordedByLogin, &e.RecordedByName, &e.PaidByLogin, &e.PaidByName,
			&e.Description, &e.Quantity, &e.Amount, &e.Identifiers, &e.TotalAfter); err != nil {
			return nil, err
		}
		list = append(list, e)
	}
	return list, rows.Err()
}

// Deliver marks the deliveries as handed out to pickedUpByID, recorded by
// recordedByID, all or nothing, and says how many units went. Each is some
// units of a row (all that are still to go when Quantity is 0), more than that
// being ErrAlreadyPickedUp. For an article with numbers the units are items:
// the given Identifiers, or else the first assigned ones not delivered yet, and
// there must be that many assigned (ErrIdentifiersMissing).
func (r *Repository) Deliver(ctx context.Context, distributionID int64, deliveries []Delivery, recordedByID, pickedUpByID int64) (int, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	total := 0
	for _, dl := range deliveries {
		var quantity, picked int
		var numbered bool
		err := tx.QueryRow(ctx, `
			SELECT n.quantity, n.picked_up_quantity, COALESCE(a.has_identifiers, false)
			FROM distribution_units n LEFT JOIN distribution_articles a ON a.id = n.article_id
			WHERE n.id = $1 AND n.distribution_id = $2 FOR UPDATE OF n
		`, dl.UnitID, distributionID).Scan(&quantity, &picked, &numbered)
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, ErrUnitNotFound
		}
		if err != nil {
			return 0, err
		}
		remaining := quantity - picked
		n := dl.Quantity
		var pending []string
		if numbered {
			if pending, err = markedItems(ctx, tx, dl.UnitID, "", "delivered_at"); err != nil {
				return 0, err
			}
		}
		if len(dl.Identifiers) > 0 {
			if dl.Quantity != 0 && dl.Quantity != len(dl.Identifiers) {
				return 0, ErrInvalidQuantity
			}
			n = len(dl.Identifiers)
		} else if n == 0 {
			// All that can go: all that is left, or, in an article with numbers,
			// all that has one assigned.
			n = remaining
			if numbered {
				n = len(pending)
				if n == 0 && remaining > 0 {
					return 0, ErrIdentifiersMissing
				}
			}
		}
		if n < 1 || n > remaining {
			return 0, ErrAlreadyPickedUp
		}
		if numbered {
			chosen := dl.Identifiers
			if len(chosen) == 0 {
				if len(pending) < n {
					return 0, ErrIdentifiersMissing
				}
				chosen = pending[:n]
			}
			if err := checkAmong(ctx, tx, dl.UnitID, chosen, pending, ErrAlreadyPickedUp); err != nil {
				return 0, err
			}
			if _, err := tx.Exec(ctx, `
				UPDATE distribution_unit_identifiers SET delivered_at = now() WHERE unit_id = $1 AND identifier = ANY($2)
			`, dl.UnitID, chosen); err != nil {
				return 0, err
			}
		} else if len(dl.Identifiers) > 0 {
			return 0, ErrInvalidIdentifiers
		}
		if _, err := tx.Exec(ctx, `
			UPDATE distribution_units SET picked_up_quantity = picked_up_quantity + $2, picked_up_at = now(),
				pickup_recorded_by_id = $3, picked_up_by_id = $4
			WHERE id = $1
		`, dl.UnitID, n, recordedByID, pickedUpByID); err != nil {
			return 0, err
		}
		total += n
	}
	return total, tx.Commit(ctx)
}

// checkAmong checks that every number in specified is among available (the ones in
// the state the operation needs). Otherwise it says which aren't: first those the
// row doesn't have at all (ErrIdentifierNotAssigned), else those it has in
// another state (wrongState).
func checkAmong(ctx context.Context, tx pgx.Tx, unitID int64, specified, available []string, wrongState error) error {
	var missing []string
	for _, identifier := range specified {
		if !slices.Contains(available, identifier) {
			missing = append(missing, identifier)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	assigned, err := markedItems(ctx, tx, unitID, "", "")
	if err != nil {
		return err
	}
	var absent []string
	for _, identifier := range missing {
		if !slices.Contains(assigned, identifier) {
			absent = append(absent, identifier)
		}
	}
	if len(absent) > 0 {
		return fmt.Errorf("%w: %s", ErrIdentifierNotAssigned, strings.Join(absent, ", "))
	}
	return fmt.Errorf("%w: %s", wrongState, strings.Join(missing, ", "))
}

// markedItems lists the identifiers of a row, in the order they were assigned,
// that have the `with` mark (a column: delivered_at, paid_at) and do not have the
// `without` one; either may be empty for no condition.
func markedItems(ctx context.Context, tx pgx.Tx, unitID int64, with, without string) ([]string, error) {
	where := "unit_id = $1"
	if with != "" {
		where += " AND " + with + " IS NOT NULL"
	}
	if without != "" {
		where += " AND " + without + " IS NULL"
	}
	rows, err := tx.Query(ctx, `SELECT identifier FROM distribution_unit_identifiers WHERE `+where+` ORDER BY id`, unitID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var identifier string
		if err := rows.Scan(&identifier); err != nil {
			return nil, err
		}
		out = append(out, identifier)
	}
	return out, rows.Err()
}

// unitState is what moving units of a row needs to know.
type unitState struct {
	quantity, picked, paid int
	numbered               bool
}

// lockUnit reads and locks a row of the distribution.
func lockUnit(ctx context.Context, tx pgx.Tx, distributionID, id int64) (unitState, error) {
	var st unitState
	err := tx.QueryRow(ctx, `
		SELECT n.quantity, n.picked_up_quantity, n.paid_quantity, COALESCE(a.has_identifiers, false)
		FROM distribution_units n LEFT JOIN distribution_articles a ON a.id = n.article_id
		WHERE n.id = $1 AND n.distribution_id = $2 FOR UPDATE OF n
	`, id, distributionID).Scan(&st.quantity, &st.picked, &st.paid, &st.numbered)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrUnitNotFound
	}
	return st, err
}

// takeLast chooses which of the available items an operation on q of them is
// about: the given ones, which have to be among them (notAmong is the error
// otherwise), or else the last q.
func takeLast(available, specified []string, q int, notAmong error) ([]string, error) {
	if len(specified) > 0 {
		for _, identifier := range specified {
			if !slices.Contains(available, identifier) {
				return nil, notAmong
			}
		}
		return specified, nil
	}
	if q > len(available) {
		return nil, ErrInvalidQuantity
	}
	return available[len(available)-q:], nil
}

// ClearPickup undoes a delivery: quantity units of the row (all that were
// handed out when 0), or the given delivered Identifiers, go back to not
// being handed out. The numbers stay assigned to the row. Undoing more than
// was delivered is ErrInvalidQuantity.
func (r *Repository) ClearPickup(ctx context.Context, distributionID, id int64, quantity int, identifiers []string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	st, err := lockUnit(ctx, tx, distributionID, id)
	if err != nil {
		return err
	}
	if st.numbered && len(identifiers) > 0 {
		delivered, err := markedItems(ctx, tx, id, "delivered_at", "")
		if err != nil {
			return err
		}
		if err := checkAmong(ctx, tx, id, identifiers, delivered, ErrNotDelivered); err != nil {
			return err
		}
	}
	q := quantity
	if len(identifiers) > 0 {
		q = len(identifiers)
	} else if q == 0 {
		q = st.picked
	}
	if q == 0 {
		return nil
	}
	if q < 1 || q > st.picked || (len(identifiers) > 0 && !st.numbered) {
		return ErrInvalidQuantity
	}
	if st.numbered {
		delivered, err := markedItems(ctx, tx, id, "delivered_at", "")
		if err != nil {
			return err
		}
		taken, err := takeLast(delivered, identifiers, q, ErrInvalidIdentifiers)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE distribution_unit_identifiers SET delivered_at = NULL WHERE unit_id = $1 AND identifier = ANY($2)`, id, taken); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE distribution_units SET picked_up_quantity = picked_up_quantity - $2,
			picked_up_at = CASE WHEN picked_up_quantity - $2 = 0 THEN NULL ELSE picked_up_at END,
			pickup_recorded_by_id = CASE WHEN picked_up_quantity - $2 = 0 THEN NULL ELSE pickup_recorded_by_id END,
			picked_up_by_id = CASE WHEN picked_up_quantity - $2 = 0 THEN NULL ELSE picked_up_by_id END
		WHERE id = $1
	`, id, q); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ReturnUnits takes back units that were handed out and aren't paid: quantity of
// them (all that can be, when 0) or the given delivered Identifiers. They stop
// being part of the row — it gets that much smaller, and is deleted when nothing
// is left — and their numbers are free again. In an article with numbers it is
// the items that are paid, so a paid one can't be returned (ErrBelowPaid); in
// one without, the row can't end up smaller than what is paid. More than was
// handed out is ErrInvalidQuantity.
func (r *Repository) ReturnUnits(ctx context.Context, distributionID, id int64, quantity int, identifiers []string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	st, err := lockUnit(ctx, tx, distributionID, id)
	if err != nil {
		return err
	}
	var taken []string
	q := quantity
	if st.numbered {
		returnable, err := markedItems(ctx, tx, id, "delivered_at", "paid_at")
		if err != nil {
			return err
		}
		delivered, err := markedItems(ctx, tx, id, "delivered_at", "")
		if err != nil {
			return err
		}
		if err := checkAmong(ctx, tx, id, identifiers, delivered, ErrNotDelivered); err != nil {
			return err
		}
		// Delivered but paid: a paid number can't be returned.
		if err := checkAmong(ctx, tx, id, identifiers, returnable, ErrBelowPaid); err != nil {
			return err
		}
		if len(identifiers) > 0 {
			q = len(identifiers)
		} else if q == 0 {
			q = len(returnable)
		}
		if q < 1 || q > st.picked {
			return ErrInvalidQuantity
		}
		if len(identifiers) == 0 && q > len(returnable) {
			return ErrBelowPaid
		}
		if taken, err = takeLast(returnable, identifiers, q, ErrInvalidIdentifiers); err != nil {
			return err
		}
	} else {
		if len(identifiers) > 0 {
			return ErrInvalidQuantity
		}
		if q == 0 {
			q = min(st.picked, st.quantity-st.paid)
		}
		if q < 1 || q > st.picked {
			return ErrInvalidQuantity
		}
		if q > st.quantity-st.paid {
			return ErrBelowPaid
		}
	}
	if len(taken) > 0 {
		if _, err := tx.Exec(ctx, `DELETE FROM distribution_unit_identifiers WHERE unit_id = $1 AND identifier = ANY($2)`, id, taken); err != nil {
			return err
		}
	}
	if st.quantity-q == 0 {
		if _, err := tx.Exec(ctx, `DELETE FROM distribution_units WHERE id = $1`, id); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE distribution_units SET quantity = quantity - $2, picked_up_quantity = picked_up_quantity - $2,
			picked_up_at = CASE WHEN picked_up_quantity - $2 = 0 THEN NULL ELSE picked_up_at END,
			pickup_recorded_by_id = CASE WHEN picked_up_quantity - $2 = 0 THEN NULL ELSE pickup_recorded_by_id END,
			picked_up_by_id = CASE WHEN picked_up_quantity - $2 = 0 THEN NULL ELSE picked_up_by_id END
		WHERE id = $1
	`, id, q); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// MarkPaid adds the given payments to their rows, all or nothing, and says
// how many units were marked in all. A payment is some units of a row (all that
// can be when its Quantity is 0). In a row of an article with numbers only the
// items with a number can be paid: the given Identifiers, or else the first
// assigned and unpaid (ErrIdentifiersMissing when there aren't that many); in
// any other, a count, and more than are pending, or something already fully paid,
// is ErrAlreadyPaid. paid_at and who marked it become those of this payment.
func (r *Repository) MarkPaid(ctx context.Context, distributionID int64, payments []Payment, markedByID, assetAccountID int64) (int, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	marked := 0
	var logged []loggedPayment
	for _, p := range payments {
		st, err := lockUnit(ctx, tx, distributionID, p.UnitID)
		if err != nil {
			return 0, err
		}
		n := p.Quantity
		var paidNow []string // the numbers this payment is about
		if st.numbered {
			unpaid, err := markedItems(ctx, tx, p.UnitID, "", "paid_at")
			if err != nil {
				return 0, err
			}
			chosen := p.Identifiers
			if len(chosen) > 0 {
				if p.Quantity != 0 && p.Quantity != len(chosen) {
					return 0, ErrInvalidQuantity
				}
				if err := checkAmong(ctx, tx, p.UnitID, chosen, unpaid, ErrAlreadyPaid); err != nil {
					return 0, err
				}
			} else {
				if n == 0 {
					n = len(unpaid)
				}
				if n < 1 {
					if st.paid > 0 {
						return 0, ErrAlreadyPaid
					}
					return 0, ErrIdentifiersMissing
				}
				if n > len(unpaid) {
					return 0, ErrIdentifiersMissing
				}
				chosen = unpaid[:n]
			}
			if _, err := tx.Exec(ctx, `
				UPDATE distribution_unit_identifiers SET paid_at = now(), paid_marked_by_id = $3
				WHERE unit_id = $1 AND identifier = ANY($2)
			`, p.UnitID, chosen, markedByID); err != nil {
				return 0, err
			}
			n = len(chosen)
			paidNow = chosen
		} else {
			if len(p.Identifiers) > 0 {
				return 0, ErrInvalidIdentifiers
			}
			pending := st.quantity - st.paid
			if n == 0 {
				n = pending
			}
			if n < 1 || n > pending {
				return 0, ErrAlreadyPaid
			}
		}
		if _, err := tx.Exec(ctx, `
			UPDATE distribution_units SET paid_quantity = paid_quantity + $2, paid_at = now(), paid_marked_by_id = $3 WHERE id = $1
		`, p.UnitID, n, markedByID); err != nil {
			return 0, err
		}
		l, err := logPayment(ctx, tx, p.UnitID, n, paidNow, markedByID)
		if err != nil {
			return 0, err
		}
		logged = append(logged, l)
		marked += n
	}
	if err := recordPaymentTransactions(ctx, tx, distributionID, assetAccountID, logged); err != nil {
		return 0, err
	}
	return marked, tx.Commit(ctx)
}

// ClearPaid takes quantity units off what is paid in a row (all of it when 0), or
// the given paid Identifiers in a row of an article with numbers. Taking off more
// than is paid is ErrInvalidQuantity.
func (r *Repository) ClearPaid(ctx context.Context, distributionID, id int64, quantity int, identifiers []string, byID, assetAccountID int64) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	st, err := lockUnit(ctx, tx, distributionID, id)
	if err != nil {
		return err
	}
	if st.numbered && len(identifiers) > 0 {
		paid, err := markedItems(ctx, tx, id, "paid_at", "")
		if err != nil {
			return err
		}
		if err := checkAmong(ctx, tx, id, identifiers, paid, ErrNotPaid); err != nil {
			return err
		}
	}
	q := quantity
	if len(identifiers) > 0 {
		q = len(identifiers)
	} else if q == 0 {
		q = st.paid
	}
	if q == 0 {
		return nil
	}
	if q < 1 || q > st.paid || (len(identifiers) > 0 && !st.numbered) {
		return ErrInvalidQuantity
	}
	var takenOff []string
	if st.numbered {
		paid, err := markedItems(ctx, tx, id, "paid_at", "")
		if err != nil {
			return err
		}
		taken, err := takeLast(paid, identifiers, q, ErrInvalidIdentifiers)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE distribution_unit_identifiers SET paid_at = NULL, paid_marked_by_id = NULL WHERE unit_id = $1 AND identifier = ANY($2)`, id, taken); err != nil {
			return err
		}
		takenOff = taken
	}
	if _, err := tx.Exec(ctx, `
		UPDATE distribution_units SET paid_quantity = paid_quantity - $2,
			paid_at = CASE WHEN paid_quantity - $2 = 0 THEN NULL ELSE paid_at END,
			paid_marked_by_id = CASE WHEN paid_quantity - $2 = 0 THEN NULL ELSE paid_marked_by_id END
		WHERE id = $1
	`, id, q); err != nil {
		return err
	}
	l, err := logPayment(ctx, tx, id, -q, takenOff, byID)
	if err != nil {
		return err
	}
	if err := recordPaymentTransactions(ctx, tx, distributionID, assetAccountID, []loggedPayment{l}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Order sums up what has been asked for, by combination of choices. Units
// nobody has chosen the options of yet add up under empty choices.
func (r *Repository) Order(ctx context.Context, d Distribution) (Order, error) {
	order := Order{Lines: []OrderLine{}}
	rows, err := r.pool.Query(ctx, `
		SELECT n.article_id, ar.name, n.choices, sum(n.quantity), COALESCE(sum(n.price * n.quantity), 0)::float8
		FROM distribution_units n
		LEFT JOIN distribution_articles ar ON ar.id = n.article_id
		WHERE n.distribution_id = $1
		GROUP BY n.article_id, ar.name, ar.position, n.choices
		ORDER BY ar.position NULLS FIRST, n.choices::text
	`, d.ID)
	if err != nil {
		return Order{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var line OrderLine
		var amount float64
		if err := rows.Scan(&line.ArticleID, &line.ArticleName, &line.Choices, &line.Count, &amount); err != nil {
			return Order{}, err
		}
		order.Lines = append(order.Lines, line)
		order.TotalUnits += line.Count
		order.TotalAmount += amount
	}
	if err := rows.Err(); err != nil {
		return Order{}, err
	}

	// A draft for all members has no units yet — they are made when it opens —
	// but what has to be ordered is already known: one for each member on the
	// cutoff date.
	if d.Audience == AudienceAllMembers && d.CutoffDate != nil && d.Status == StatusDraft {
		var missing int
		if err := r.pool.QueryRow(ctx, `
			SELECT count(*) FROM (SELECT DISTINCT user_id FROM memberships WHERE period @> $2::date) m
			WHERE NOT EXISTS (
				SELECT 1 FROM distribution_units n
				WHERE n.distribution_id = $1 AND n.beneficiary_id = m.user_id AND NOT n.is_extra AND n.article_id IS NULL)
		`, d.ID, d.CutoffDate).Scan(&missing); err != nil {
			return Order{}, err
		}
		order.Provisional = true
		if missing > 0 {
			order.TotalUnits += missing
			order.TotalAmount += float64(missing) * d.Price
			// Like the real ones, they have chosen nothing: they add up with the
			// other units with empty choices, if there are any (extras of an
			// article with no options), or make that line.
			merged := false
			for i := range order.Lines {
				if order.Lines[i].ArticleID == nil && len(order.Lines[i].Choices) == 0 {
					order.Lines[i].Count += missing
					merged = true
				}
			}
			if !merged {
				order.Lines = append([]OrderLine{{Choices: map[string]string{}, Count: missing}}, order.Lines...)
			}
		}
	}
	return order, nil
}

// SetIdentifiers replaces the numbers of the items of a row of an article that
// has them. A number another unit of the same article already has is
// ErrIdentifierTaken. It returns ErrInvalidIdentifiers when the unit isn't a row of
// such an article, and nothing about how many there are — the service checks that.
func (r *Repository) SetIdentifiers(ctx context.Context, distributionID, id int64, identifiers []string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var articleID int64
	err = tx.QueryRow(ctx, `
		SELECT n.article_id FROM distribution_units n
		JOIN distribution_articles a ON a.id = n.article_id
		WHERE n.id = $1 AND n.distribution_id = $2 AND a.has_identifiers
		FOR UPDATE OF n
	`, id, distributionID).Scan(&articleID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrInvalidIdentifiers
	}
	if err != nil {
		return err
	}
	// What was handed out or paid stays: the new list has to include it.
	rows, err := tx.Query(ctx, `SELECT identifier FROM distribution_unit_identifiers WHERE unit_id = $1 AND (delivered_at IS NOT NULL OR paid_at IS NOT NULL)`, id)
	if err != nil {
		return err
	}
	var delivered []string
	for rows.Next() {
		var identifier string
		if err := rows.Scan(&identifier); err != nil {
			rows.Close()
			return err
		}
		delivered = append(delivered, identifier)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, identifier := range delivered {
		if !slices.Contains(identifiers, identifier) {
			return ErrInvalidIdentifiers
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM distribution_unit_identifiers WHERE unit_id = $1 AND delivered_at IS NULL AND paid_at IS NULL`, id); err != nil {
		return err
	}
	for _, identifier := range identifiers {
		if slices.Contains(delivered, identifier) {
			continue
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO distribution_unit_identifiers (unit_id, article_id, identifier) VALUES ($1, $2, $3)
		`, id, articleID, identifier)
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolationCode {
			return ErrIdentifierTaken
		}
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// queryer is what articleIdentifiers needs: a pool or a transaction.
type queryer interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// articleIdentifiers lists every number already given for the article, whoever has it.
func articleIdentifiers(ctx context.Context, q queryer, articleID int64) ([]string, error) {
	rows, err := q.Query(ctx, `SELECT identifier FROM distribution_unit_identifiers WHERE article_id = $1 ORDER BY id`, articleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var identifier string
		if err := rows.Scan(&identifier); err != nil {
			return nil, err
		}
		out = append(out, identifier)
	}
	return out, rows.Err()
}

// ArticleIdentifiers lists every number already given for the article.
func (r *Repository) ArticleIdentifiers(ctx context.Context, articleID int64) ([]string, error) {
	return articleIdentifiers(ctx, r.pool, articleID)
}

// Holders says who has each number given for the article: identifier to unit.
func (r *Repository) Holders(ctx context.Context, articleID int64) (map[string]NumberHolder, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT i.identifier, n.id, b.login, b.first_name || ' ' || b.last_name, i.delivered_at IS NOT NULL, i.paid_at IS NOT NULL
		FROM distribution_unit_identifiers i
		JOIN distribution_units n ON n.id = i.unit_id
		JOIN users b ON b.id = n.beneficiary_id
		WHERE i.article_id = $1
	`, articleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]NumberHolder{}
	for rows.Next() {
		var identifier string
		var h NumberHolder
		if err := rows.Scan(&identifier, &h.UnitID, &h.Login, &h.Name, &h.Delivered, &h.Paid); err != nil {
			return nil, err
		}
		out[identifier] = h
	}
	return out, rows.Err()
}
