package distributions

import (
	"context"
	"errors"
	"fmt"
	"slices"
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
	// ErrUnknownUser means a given user id doesn't exist.
	ErrUnknownUser = errors.New("unknown user")
)

const (
	uniqueViolationCode     = "23505"
	foreignKeyViolationCode = "23503"
)

type Repository struct {
	pool db.PGX
}

func NewRepository(pool db.PGX) *Repository {
	return &Repository{pool: pool}
}

const distributionSelect = `
	SELECT d.id, d.name, d.description, d.audience, d.status, d.closes_at, d.cutoff_date,
	       d.price::float8, d.extra_price::float8, d.max_extra, d.created_at, u.login,
	       (SELECT COALESCE(sum(x.quantity), 0) FROM distribution_units x WHERE x.distribution_id = d.id),
	       (SELECT COALESCE(sum(x.quantity), 0) FROM distribution_units x WHERE x.distribution_id = d.id AND x.picked_up_at IS NOT NULL)
	FROM distributions d
	JOIN users u ON u.id = d.created_by`

func scanDistribution(row pgx.Row, d *Distribution) error {
	return row.Scan(&d.ID, &d.Name, &d.Description, &d.Audience, &d.Status, &d.ClosesAt, &d.CutoffDate,
		&d.Price, &d.ExtraPrice, &d.MaxExtra, &d.CreatedAt, &d.CreatedByLogin, &d.UnitCount, &d.PickedUpCount)
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

func (r *Repository) Create(ctx context.Context, in CreateInput) (Distribution, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Distribution{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var id int64
	err = tx.QueryRow(ctx, `
		INSERT INTO distributions (name, description, audience, closes_at, cutoff_date, price, extra_price, max_extra, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id
	`, in.Name, in.Description, in.Audience, in.ClosesAt, in.CutoffDate, in.Price, in.ExtraPrice, in.MaxExtra, in.CreatedBy).Scan(&id)
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

	tag, err := tx.Exec(ctx, `
		UPDATE distributions SET
			name = $2, description = $3, audience = $4, closes_at = $5, cutoff_date = $6,
			price = $7, extra_price = $8, max_extra = $9,
			status = COALESCE($10, status)
		WHERE id = $1
	`, id, in.Name, in.Description, in.Audience, in.ClosesAt, in.CutoffDate, in.Price, in.ExtraPrice, in.MaxExtra, in.Status)
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
	       n.paid_at, pm.login, n.picked_up_at, pr.login, pu.login
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
		&u.Quantity, &u.Choices, &u.Price, &u.Identifiers,
		&u.PaidAt, &u.PaidMarkedByLogin, &u.PickedUpAt, &u.PickupRecordedByLogin, &u.PickedUpByLogin)
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
// transaction. except is a unit whose own quantity doesn't count, because it is
// the one being changed.
func checkQuantity(ctx context.Context, tx pgx.Tx, articleID, except int64, quantity int) error {
	var maxTotal, perPerson *int
	var requested int
	err := tx.QueryRow(ctx, `
		SELECT a.max_total, a.max_per_person,
		       (SELECT COALESCE(sum(n.quantity), 0) FROM distribution_units n WHERE n.article_id = a.id AND n.id <> $2)
		FROM distribution_articles a WHERE a.id = $1
	`, articleID, except).Scan(&maxTotal, &perPerson, &requested)
	if err != nil {
		return err
	}
	if perPerson != nil && quantity > *perPerson {
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
	if err := checkQuantity(ctx, tx, *in.ArticleID, 0, in.Quantity); err != nil {
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
	err = tx.QueryRow(ctx, `SELECT article_id FROM distribution_units WHERE id = $1 AND distribution_id = $2`, id, distributionID).Scan(&articleID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Unit{}, ErrUnitNotFound
	}
	if err != nil {
		return Unit{}, err
	}
	if articleID == nil {
		return Unit{}, ErrInvalidQuantity
	}
	if err := checkQuantity(ctx, tx, *articleID, id, quantity); err != nil {
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

func (r *Repository) DeleteUnit(ctx context.Context, distributionID, id int64) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM distribution_units WHERE id = $1 AND distribution_id = $2`, id, distributionID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrUnitNotFound
	}
	return nil
}

// markUnits sets markColumn's own columns on every given unit, all or
// nothing: every id has to belong to the distribution (ErrUnitNotFound) and
// none may already carry the mark (alreadyErr).
func (r *Repository) markUnits(ctx context.Context, distributionID int64, ids []int64, markedColumn, setClause string, alreadyErr error, args ...any) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var found, already int
	if err := tx.QueryRow(ctx, `
		SELECT count(*), count(*) FILTER (WHERE `+markedColumn+` IS NOT NULL)
		FROM distribution_units WHERE distribution_id = $1 AND id = ANY($2)
	`, distributionID, ids).Scan(&found, &already); err != nil {
		return err
	}
	if found != len(ids) {
		return ErrUnitNotFound
	}
	if already > 0 {
		return alreadyErr
	}
	allArgs := append([]any{distributionID, ids}, args...)
	if _, err := tx.Exec(ctx, `UPDATE distribution_units SET `+setClause+` WHERE distribution_id = $1 AND id = ANY($2)`, allArgs...); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// MarkPickedUp records that the given units were handed to pickedUpByID, and
// that recordedByID wrote it down.
func (r *Repository) MarkPickedUp(ctx context.Context, distributionID int64, ids []int64, recordedByID, pickedUpByID int64) error {
	return r.markUnits(ctx, distributionID, ids, "picked_up_at",
		"picked_up_at = now(), pickup_recorded_by_id = $3, picked_up_by_id = $4", ErrAlreadyPickedUp, recordedByID, pickedUpByID)
}

func (r *Repository) ClearPickup(ctx context.Context, distributionID, id int64) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE distribution_units SET picked_up_at = NULL, pickup_recorded_by_id = NULL, picked_up_by_id = NULL
		WHERE id = $1 AND distribution_id = $2
	`, id, distributionID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrUnitNotFound
	}
	return nil
}

// MarkPaid records that the given units were paid, and by whom it was noted.
func (r *Repository) MarkPaid(ctx context.Context, distributionID int64, ids []int64, markedByID int64) error {
	return r.markUnits(ctx, distributionID, ids, "paid_at",
		"paid_at = now(), paid_marked_by_id = $3", ErrAlreadyPaid, markedByID)
}

func (r *Repository) ClearPaid(ctx context.Context, distributionID, id int64) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE distribution_units SET paid_at = NULL, paid_marked_by_id = NULL
		WHERE id = $1 AND distribution_id = $2
	`, id, distributionID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrUnitNotFound
	}
	return nil
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
	if _, err := tx.Exec(ctx, `DELETE FROM distribution_unit_identifiers WHERE unit_id = $1`, id); err != nil {
		return err
	}
	for _, identifier := range identifiers {
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

// MissingIdentifiers counts the given units that are rows of an article with
// numbers and don't have as many as their quantity yet.
func (r *Repository) MissingIdentifiers(ctx context.Context, distributionID int64, ids []int64) (int, error) {
	var missing int
	err := r.pool.QueryRow(ctx, `
		SELECT count(*) FROM distribution_units n
		JOIN distribution_articles a ON a.id = n.article_id
		WHERE n.distribution_id = $1 AND n.id = ANY($2) AND a.has_identifiers
		  AND (SELECT count(*) FROM distribution_unit_identifiers i WHERE i.unit_id = n.id) <> n.quantity
	`, distributionID, ids).Scan(&missing)
	return missing, err
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
		SELECT i.identifier, n.id, b.login, b.first_name || ' ' || b.last_name
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
		if err := rows.Scan(&identifier, &h.UnitID, &h.Login, &h.Name); err != nil {
			return nil, err
		}
		out[identifier] = h
	}
	return out, rows.Err()
}
