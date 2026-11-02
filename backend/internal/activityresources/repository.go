package activityresources

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/platform/db"
)

var (
	ErrNotFound = errors.New("resource not found")
	// ErrInvalidFields means a text/description or file-field combination
	// that doesn't match the resource's own type was given — the
	// database's own CHECK constraints (migration
	// 00010_activity_resources.sql) are the actual source of truth for
	// which combinations are valid; this just
	// surfaces a constraint violation as a typed error instead of a raw
	// one, the same way material.CreateMovement does for
	// activityRequiredConstraint.
	ErrInvalidFields = errors.New("fields given don't match this resource's type")
	// ErrReorderMismatch means the given ids aren't exactly this
	// activity's current set of resources — see Repository.Reorder.
	ErrReorderMismatch = errors.New("given ids don't match this activity's current resources")
)

const checkViolationCode = "23514"

// resourceSelect is appended to every query that returns one or more
// Resource rows other than Create's INSERT ... RETURNING (which can't
// JOIN against the row it just wrote — see Repository.Create) — a
// listing can span resources contributed by different people, so the
// author's login is joined in rather than resolved one at a time.
const resourceSelect = `
	ar.id, ar.activity_id, ar.type, ar.author_id, au.login, ar.title, ar.description,
	ar.file_name, ar.file_size, ar.content_type, ar.position, ar.created_at
	FROM activity_resources ar
	JOIN users au ON au.id = ar.author_id`

func scanResource(row interface{ Scan(dest ...any) error }, res *Resource) error {
	return row.Scan(&res.ID, &res.ActivityID, &res.Type, &res.AuthorID, &res.AuthorLogin, &res.Title,
		&res.Description, &res.FileName, &res.FileSize, &res.ContentType, &res.Position, &res.CreatedAt)
}

type Repository struct {
	pool db.PGX
}

func NewRepository(pool db.PGX) *Repository {
	return &Repository{pool: pool}
}

// Create appends the new resource after whatever the activity's current
// highest position is (0, so position 1, for its first resource) — the
// MAX(position) subquery and the INSERT run as one statement, so there's
// no separate round trip that a concurrent create could race against.
func (r *Repository) Create(ctx context.Context, in CreateInput) (Resource, error) {
	var res Resource
	err := r.pool.QueryRow(ctx, `
		INSERT INTO activity_resources (activity_id, type, author_id, title, description, file_name, file_size, content_type, position)
		SELECT $1, $2, $3, $4, $5, $6, $7, $8, COALESCE(MAX(position), 0) + 1
		FROM activity_resources WHERE activity_id = $1
		RETURNING id, activity_id, type, author_id, title, description, file_name, file_size, content_type, position, created_at
	`, in.ActivityID, in.Type, in.AuthorID, in.Title, in.Description, in.FileName, in.FileSize, in.ContentType,
	).Scan(&res.ID, &res.ActivityID, &res.Type, &res.AuthorID, &res.Title, &res.Description,
		&res.FileName, &res.FileSize, &res.ContentType, &res.Position, &res.CreatedAt)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == checkViolationCode {
		return Resource{}, ErrInvalidFields
	}
	return res, err
}

func (r *Repository) GetByID(ctx context.Context, id int64) (Resource, error) {
	var res Resource
	err := scanResource(r.pool.QueryRow(ctx, `
		SELECT `+resourceSelect+`
		WHERE ar.id = $1
	`, id), &res)
	if errors.Is(err, pgx.ErrNoRows) {
		return Resource{}, ErrNotFound
	}
	return res, err
}

// ListByActivity returns every resource for activityID in memoria
// order (Resource.Position, ascending) — the order a caller last set
// via Reorder, or creation order for one that's never been reordered.
func (r *Repository) ListByActivity(ctx context.Context, activityID int64) ([]Resource, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+resourceSelect+`
		WHERE ar.activity_id = $1
		ORDER BY ar.position
	`, activityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []Resource{}
	for rows.Next() {
		var res Resource
		if err := scanResource(rows, &res); err != nil {
			return nil, err
		}
		list = append(list, res)
	}
	return list, rows.Err()
}

// Update edits only title and description (see UpdateInput) — the
// database CHECK constraints still apply, so trying to set a
// description on a file resource comes back as ErrInvalidFields, not
// silently ignored.
func (r *Repository) Update(ctx context.Context, id int64, in UpdateInput) (Resource, error) {
	res, err := r.GetByID(ctx, id)
	if err != nil {
		return Resource{}, err
	}
	if in.Title != nil {
		res.Title = *in.Title
	}
	if in.Description != nil {
		res.Description = in.Description
	}
	_, err = r.pool.Exec(ctx, `
		UPDATE activity_resources SET title = $1, description = $2 WHERE id = $3
	`, res.Title, res.Description, id)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == checkViolationCode {
		return Resource{}, ErrInvalidFields
	}
	if err != nil {
		return Resource{}, err
	}
	return res, nil
}

func (r *Repository) Delete(ctx context.Context, id int64) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM activity_resources WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Reorder rewrites every resource's position to its index (1-based) in
// orderedIDs — the whole memoria's new order in one call, rather than a
// move-this-one-item endpoint that would need to shuffle the rest
// itself. orderedIDs must be exactly activityID's current set of
// resource ids (same members, no more, no fewer) or this fails with
// ErrReorderMismatch instead of silently dropping or ignoring stray
// ids — a stale client (someone else added or removed a resource since
// this list was fetched) should have to reload and retry, not overwrite
// positions using an order that no longer matches what's actually there.
func (r *Repository) Reorder(ctx context.Context, activityID int64, orderedIDs []int64) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx, `
		SELECT id FROM activity_resources WHERE activity_id = $1 FOR UPDATE
	`, activityID)
	if err != nil {
		return err
	}
	current := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		current[id] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()

	if len(orderedIDs) != len(current) {
		return ErrReorderMismatch
	}
	seen := make(map[int64]bool, len(orderedIDs))
	for _, id := range orderedIDs {
		if !current[id] || seen[id] {
			return ErrReorderMismatch
		}
		seen[id] = true
	}

	for i, id := range orderedIDs {
		if _, err := tx.Exec(ctx, `
			UPDATE activity_resources SET position = $1 WHERE id = $2
		`, i+1, id); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
