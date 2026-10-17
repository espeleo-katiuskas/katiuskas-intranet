package distributions

import (
	"context"
	"errors"
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
	       (SELECT count(*) FROM distribution_units x WHERE x.distribution_id = d.id),
	       (SELECT count(*) FROM distribution_units x WHERE x.distribution_id = d.id AND x.picked_up_at IS NOT NULL)
	FROM distributions d
	JOIN users u ON u.id = d.created_by`

func scanDistribution(row pgx.Row, d *Distribution) error {
	return row.Scan(&d.ID, &d.Name, &d.Description, &d.Audience, &d.Status, &d.ClosesAt, &d.CutoffDate,
		&d.Price, &d.ExtraPrice, &d.MaxExtra, &d.CreatedAt, &d.CreatedByLogin, &d.UnitCount, &d.PickedUpCount)
}

// attachOptions fills in Options for every given distribution, in one query.
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
	return rows.Err()
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
	if err := tx.Commit(ctx); err != nil {
		return Distribution{}, err
	}
	return r.Get(ctx, id)
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

// Delete removes a distribution nobody has asked for yet.
func (r *Repository) Delete(ctx context.Context, id int64) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var units int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM distribution_units WHERE distribution_id = $1`, id).Scan(&units); err != nil {
		return err
	}
	if units > 0 {
		return ErrHasUnits
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

const unitSelect = `
	SELECT n.id, n.distribution_id, n.beneficiary_id, b.login, b.first_name || ' ' || b.last_name,
	       n.registered_by_id, rb.login, n.registered_at, n.is_extra, n.choices, n.price::float8,
	       n.paid_at, pm.login, n.picked_up_at, pr.login, pu.login
	FROM distribution_units n
	JOIN users b ON b.id = n.beneficiary_id
	JOIN users rb ON rb.id = n.registered_by_id
	LEFT JOIN users pm ON pm.id = n.paid_marked_by_id
	LEFT JOIN users pr ON pr.id = n.pickup_recorded_by_id
	LEFT JOIN users pu ON pu.id = n.picked_up_by_id`

func scanUnit(row pgx.Row, u *Unit) error {
	return row.Scan(&u.ID, &u.DistributionID, &u.BeneficiaryID, &u.BeneficiaryLogin, &u.BeneficiaryName,
		&u.RegisteredByID, &u.RegisteredByLogin, &u.RegisteredAt, &u.IsExtra, &u.Choices, &u.Price,
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
		ORDER BY b.last_name, b.first_name, n.beneficiary_id, n.is_extra, n.id
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

// UpdateChoices changes what was chosen for a unit and nothing else: who
// entered it and when stay as they were.
func (r *Repository) UpdateChoices(ctx context.Context, distributionID, id int64, choices map[string]string) (Unit, error) {
	tag, err := r.pool.Exec(ctx, `
		UPDATE distribution_units SET choices = $3 WHERE id = $1 AND distribution_id = $2
	`, id, distributionID, choices)
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

// Order sums up what has been asked for. pending (for a distribution for all
// members) are the people it is for who have no unit of their own yet.
func (r *Repository) Order(ctx context.Context, d Distribution) (Order, error) {
	order := Order{Lines: []OrderLine{}, PendingMembers: []PendingMember{}}
	rows, err := r.pool.Query(ctx, `
		SELECT choices, count(*), COALESCE(sum(price), 0)::float8
		FROM distribution_units WHERE distribution_id = $1
		GROUP BY choices ORDER BY choices::text
	`, d.ID)
	if err != nil {
		return Order{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var line OrderLine
		var amount float64
		if err := rows.Scan(&line.Choices, &line.Count, &amount); err != nil {
			return Order{}, err
		}
		order.Lines = append(order.Lines, line)
		order.TotalUnits += line.Count
		order.TotalAmount += amount
	}
	if err := rows.Err(); err != nil {
		return Order{}, err
	}

	if d.Audience == AudienceAllMembers && d.CutoffDate != nil {
		pending, err := r.pool.Query(ctx, `
			SELECT u.login, u.first_name || ' ' || u.last_name
			FROM users u
			WHERE EXISTS (SELECT 1 FROM memberships m WHERE m.user_id = u.id AND m.period @> $1::date)
			  AND NOT EXISTS (
			      SELECT 1 FROM distribution_units n
			      WHERE n.distribution_id = $2 AND n.beneficiary_id = u.id AND NOT n.is_extra)
			ORDER BY u.last_name, u.first_name, u.id
		`, d.CutoffDate, d.ID)
		if err != nil {
			return Order{}, err
		}
		defer pending.Close()
		for pending.Next() {
			var p PendingMember
			if err := pending.Scan(&p.Login, &p.Name); err != nil {
				return Order{}, err
			}
			order.PendingMembers = append(order.PendingMembers, p)
		}
		if err := pending.Err(); err != nil {
			return Order{}, err
		}
	}
	return order, nil
}
