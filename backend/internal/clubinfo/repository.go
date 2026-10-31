package clubinfo

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/platform/db"
)

var (
	ErrNotFound = errors.New("club info item not found")
	// ErrReorderMismatch means the given ids aren't exactly the current set of items.
	ErrReorderMismatch = errors.New("given ids don't match the current items")
)

type Repository struct {
	pool db.PGX
}

func NewRepository(pool db.PGX) *Repository {
	return &Repository{pool: pool}
}

const itemColumns = `id, position, label, value, copy_value, copy_label`

func scanItem(row pgx.Row, it *Item) error {
	return row.Scan(&it.ID, &it.Position, &it.Label, &it.Value, &it.CopyValue, &it.CopyLabel)
}

func (r *Repository) List(ctx context.Context) ([]Item, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+itemColumns+` FROM club_info_items ORDER BY position, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Item{}
	for rows.Next() {
		var it Item
		if err := scanItem(rows, &it); err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	return items, rows.Err()
}

func (r *Repository) Get(ctx context.Context, id int64) (Item, error) {
	var it Item
	err := scanItem(r.pool.QueryRow(ctx, `SELECT `+itemColumns+` FROM club_info_items WHERE id = $1`, id), &it)
	if errors.Is(err, pgx.ErrNoRows) {
		return Item{}, ErrNotFound
	}
	return it, err
}

// Create appends the item after the last one.
func (r *Repository) Create(ctx context.Context, in Input) (Item, error) {
	var it Item
	err := scanItem(r.pool.QueryRow(ctx, `
		INSERT INTO club_info_items (position, label, value, copy_value, copy_label)
		VALUES ((SELECT COALESCE(MAX(position), 0) + 1 FROM club_info_items), $1, $2, $3, $4)
		RETURNING `+itemColumns, in.Label, in.Value, in.CopyValue, in.CopyLabel), &it)
	return it, err
}

func (r *Repository) Update(ctx context.Context, id int64, in Input) (Item, error) {
	var it Item
	err := scanItem(r.pool.QueryRow(ctx, `
		UPDATE club_info_items SET label = $2, value = $3, copy_value = $4, copy_label = $5
		WHERE id = $1
		RETURNING `+itemColumns, id, in.Label, in.Value, in.CopyValue, in.CopyLabel), &it)
	if errors.Is(err, pgx.ErrNoRows) {
		return Item{}, ErrNotFound
	}
	return it, err
}

func (r *Repository) Delete(ctx context.Context, id int64) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM club_info_items WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Reorder rewrites every item's position to its place in orderedIDs, which has to name exactly the
// current items, each once (ErrReorderMismatch otherwise) — the same "replace the whole order"
// shape the memoria's resources have, rather than a move-this-one endpoint.
func (r *Repository) Reorder(ctx context.Context, orderedIDs []int64) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	rows, err := tx.Query(ctx, `SELECT id FROM club_info_items FOR UPDATE`)
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
		if _, err := tx.Exec(ctx, `UPDATE club_info_items SET position = $1 WHERE id = $2`, i+1, id); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
