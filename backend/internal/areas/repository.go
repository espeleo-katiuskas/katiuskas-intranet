package areas

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/platform/db"
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

func pgCode(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}

// areaSelect is a zone with how many zones are below it and how many places are in it.
const areaSelect = `
	SELECT a.id, a.parent_id, a.kind, a.name, a.code, a.created_by,
		(SELECT count(*) FROM areas c WHERE c.parent_id = a.id),
		(SELECT count(*) FROM places p WHERE p.area_id = a.id)
	FROM areas a`

func scanArea(row pgx.Row, a *Area) error {
	return row.Scan(&a.ID, &a.ParentID, &a.Kind, &a.Name, &a.Code, &a.CreatedByID, &a.ChildCount, &a.PlaceCount)
}

func (r *Repository) collect(rows pgx.Rows) ([]Area, error) {
	defer rows.Close()
	list := []Area{}
	for rows.Next() {
		var a Area
		if err := scanArea(rows, &a); err != nil {
			return nil, err
		}
		list = append(list, a)
	}
	return list, rows.Err()
}

// Children are the zones directly below a zone, or the countries if there is none, by name.
func (r *Repository) Children(ctx context.Context, parentID *int64) ([]Area, error) {
	rows, err := r.pool.Query(ctx, areaSelect+` WHERE a.parent_id IS NOT DISTINCT FROM $1 ORDER BY a.norm_name, a.id`, parentID)
	if err != nil {
		return nil, err
	}
	return r.collect(rows)
}

// Search is the zones whose normalized name contains the normalized text, the exact ones first, then
// those that start with it, with their ancestors.
func (r *Repository) Search(ctx context.Context, normalized string, limit int) ([]Area, error) {
	rows, err := r.pool.Query(ctx, areaSelect+`
		WHERE a.norm_name LIKE '%' || $1 || '%'
		ORDER BY (a.norm_name = $1) DESC, (a.norm_name LIKE $1 || '%') DESC, length(a.norm_name), a.norm_name, a.id
		LIMIT $2`, normalized, limit)
	if err != nil {
		return nil, err
	}
	list, err := r.collect(rows)
	if err != nil {
		return nil, err
	}
	return list, r.addAncestors(ctx, list)
}

// addAncestors fills in the ancestors of each zone of the list, the country first.
func (r *Repository) addAncestors(ctx context.Context, list []Area) error {
	if len(list) == 0 {
		return nil
	}
	ids := make([]int64, len(list))
	for i, a := range list {
		ids[i] = a.ID
	}
	rows, err := r.pool.Query(ctx, `
		WITH RECURSIVE up AS (
			SELECT a.id AS leaf, a.parent_id AS id, 1 AS depth FROM areas a WHERE a.id = ANY($1)
			UNION ALL
			SELECT up.leaf, a.parent_id, up.depth + 1 FROM up JOIN areas a ON a.id = up.id WHERE a.parent_id IS NOT NULL
		)
		SELECT up.leaf, z.id, z.kind, z.name
		FROM up JOIN areas z ON z.id = up.id
		ORDER BY up.leaf, up.depth DESC`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	byLeaf := map[int64][]Ref{}
	for rows.Next() {
		var leaf int64
		var ref Ref
		if err := rows.Scan(&leaf, &ref.ID, &ref.Kind, &ref.Name); err != nil {
			return err
		}
		byLeaf[leaf] = append(byLeaf[leaf], ref)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range list {
		list[i].Ancestors = byLeaf[list[i].ID]
	}
	return nil
}

// Get is a zone with its ancestors.
func (r *Repository) Get(ctx context.Context, id int64) (Area, error) {
	var a Area
	err := scanArea(r.pool.QueryRow(ctx, areaSelect+` WHERE a.id = $1`, id), &a)
	if errors.Is(err, pgx.ErrNoRows) {
		return Area{}, ErrNotFound
	}
	if err != nil {
		return Area{}, err
	}
	list := []Area{a}
	if err := r.addAncestors(ctx, list); err != nil {
		return Area{}, err
	}
	return list[0], nil
}

// IDByCode is the zone with an official code: of the given kind, or a country (a zone below none) when
// kind is empty. Codes are only unique within a kind (a community and a province can share one).
func (r *Repository) IDByCode(ctx context.Context, kind, code string) (int64, error) {
	var id int64
	var err error
	if kind == "" {
		err = r.pool.QueryRow(ctx, `SELECT id FROM areas WHERE parent_id IS NULL AND code = $1 LIMIT 1`, code).Scan(&id)
	} else {
		err = r.pool.QueryRow(ctx, `SELECT id FROM areas WHERE kind = $1 AND code = $2 LIMIT 1`, kind, code).Scan(&id)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	}
	return id, err
}

// KindBelow is the kind that most of the zones directly below a zone have, or "" if there are none.
func (r *Repository) KindBelow(ctx context.Context, parentID int64) (string, error) {
	var kind string
	err := r.pool.QueryRow(ctx, `
		SELECT kind FROM areas WHERE parent_id = $1 GROUP BY kind ORDER BY count(*) DESC, kind LIMIT 1
	`, parentID).Scan(&kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return kind, err
}

// NameTaken says whether another zone below the same one (a country, if parentID is nil) has that
// normalized name; `except` is a zone that does not count (the one being renamed).
func (r *Repository) NameTaken(ctx context.Context, parentID *int64, normalized string, except int64) (bool, error) {
	var taken bool
	err := r.pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM areas WHERE parent_id IS NOT DISTINCT FROM $1 AND norm_name = $2 AND id <> $3)
	`, parentID, normalized, except).Scan(&taken)
	return taken, err
}

// Create adds a zone, saying who (a user id) added it.
func (r *Repository) Create(ctx context.Context, parentID *int64, kind, name, normalized string, createdBy int64) (Area, error) {
	var id int64
	err := r.pool.QueryRow(ctx, `
		INSERT INTO areas (parent_id, kind, name, norm_name, created_by) VALUES ($1, $2, $3, $4, $5) RETURNING id
	`, parentID, kind, name, normalized, createdBy).Scan(&id)
	switch pgCode(err) {
	case uniqueViolationCode:
		return Area{}, ErrDuplicate
	case foreignKeyViolationCode:
		return Area{}, ErrUnknownParent
	}
	if err != nil {
		return Area{}, err
	}
	return r.Get(ctx, id)
}

// Update renames a zone and changes its kind and, with move.Set, its parent, in one transaction. A zone
// cannot go below itself or below a zone that is below it: the chain of ancestors of the new parent,
// read under the lock of the row, says so.
func (r *Repository) Update(ctx context.Context, id int64, kind, name, normalized string, move Move) (Area, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Area{}, err
	}
	defer tx.Rollback(ctx)
	var parent *int64
	err = tx.QueryRow(ctx, `SELECT parent_id FROM areas WHERE id = $1 FOR UPDATE`, id).Scan(&parent)
	if errors.Is(err, pgx.ErrNoRows) {
		return Area{}, ErrNotFound
	}
	if err != nil {
		return Area{}, err
	}
	if move.Set {
		parent = move.ID
		if parent != nil {
			var below bool
			if err := tx.QueryRow(ctx, `
				WITH RECURSIVE up AS (
					SELECT id, parent_id FROM areas WHERE id = $1
					UNION ALL
					SELECT a.id, a.parent_id FROM areas a JOIN up ON a.id = up.parent_id
				)
				SELECT EXISTS (SELECT 1 FROM up WHERE id = $2)
			`, *parent, id).Scan(&below); err != nil {
				return Area{}, err
			}
			if below {
				return Area{}, ErrMoveCycle
			}
		}
	}
	_, err = tx.Exec(ctx, `UPDATE areas SET parent_id = $2, kind = $3, name = $4, norm_name = $5 WHERE id = $1`,
		id, parent, kind, name, normalized)
	switch pgCode(err) {
	case uniqueViolationCode:
		return Area{}, ErrDuplicate
	case foreignKeyViolationCode:
		return Area{}, ErrUnknownParent
	}
	if err != nil {
		return Area{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Area{}, err
	}
	return r.Get(ctx, id)
}

// Delete removes a zone that has no zones below it and no places: the foreign keys refuse otherwise, and
// the checks before them say which of the two it is.
func (r *Repository) Delete(ctx context.Context, id int64) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM areas WHERE id = $1`, id)
	if pgCode(err) == foreignKeyViolationCode {
		return ErrInUse
	}
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Merge moves the zones below `from` and the places in it to `into`, and deletes `from`, in one
// transaction. Both have to be below the same zone (or both be countries), and none of the zones
// below `from` can have the name of one below `into`. If `into` has no official code and `from` has
// one, `into` takes it.
func (r *Repository) Merge(ctx context.Context, from, into int64) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// Both rows are locked, always in the same order, so that two merges do not wait for each other.
	first, second := from, into
	if second < first {
		first, second = second, first
	}
	parents := map[int64]*int64{}
	for _, id := range []int64{first, second} {
		var parent *int64
		err := tx.QueryRow(ctx, `SELECT parent_id FROM areas WHERE id = $1 FOR UPDATE`, id).Scan(&parent)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		parents[id] = parent
	}
	// Two countries have no zone above them: both are below the root, so they can be merged.
	if (parents[from] == nil) != (parents[into] == nil) ||
		(parents[from] != nil && *parents[from] != *parents[into]) {
		return ErrMergeParents
	}
	var clash bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM areas c JOIN areas t ON t.parent_id = $2 AND t.norm_name = c.norm_name WHERE c.parent_id = $1)
	`, from, into).Scan(&clash); err != nil {
		return err
	}
	if clash {
		return ErrDuplicate
	}
	for _, statement := range []string{
		`UPDATE areas SET parent_id = $2 WHERE parent_id = $1`,
		`UPDATE places SET area_id = $2 WHERE area_id = $1`,
		`UPDATE osm_elements SET area_id = $2 WHERE area_id = $1`,
		`UPDATE areas SET code = (SELECT code FROM areas WHERE id = $1) WHERE id = $2 AND code IS NULL`,
		`DELETE FROM areas WHERE id = $1`,
	} {
		args := []any{from}
		if statement[:6] != "DELETE" {
			args = append(args, into)
		}
		if _, err := tx.Exec(ctx, statement, args...); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
