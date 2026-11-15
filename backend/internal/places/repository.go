package places

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

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == uniqueViolationCode
}

func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == foreignKeyViolationCode
}

// ----- places -----

const listColumns = `
	p.id, p.type_id, t.name, p.kind, p.name, p.alt_names, p.state, p.area_id, w.ids, w.where_text,
	p.lat, p.lon, p.altitude_m, p.depth_m, p.horizontal_length_m, p.geology_id, g.name`

// listFrom is a place with its type and, through the tree of zones, the ids and the names of the zone it
// is in and of the ones it is in, from the most specific to the country (depth 0 is the zone itself).
const listFrom = `
	FROM places p
	JOIN place_types t ON t.id = p.type_id
	LEFT JOIN place_geologies g ON g.id = p.geology_id
	JOIN LATERAL (
		WITH RECURSIVE up AS (
			SELECT a.id, a.parent_id, a.name, 0 AS depth FROM areas a WHERE a.id = p.area_id
			UNION ALL
			SELECT a.id, a.parent_id, a.name, up.depth + 1 FROM areas a JOIN up ON a.id = up.parent_id
		)
		SELECT array_agg(id ORDER BY depth DESC) AS ids, string_agg(name, ', ' ORDER BY depth) AS where_text FROM up
	) w ON true`

func (it *ListItem) scanTargets() []any {
	return []any{
		&it.ID, &it.TypeID, &it.TypeName, &it.Kind, &it.Name, &it.AltNames, &it.State, &it.AreaID,
		&it.AreaIDs, &it.Where, &it.Lat, &it.Lon, &it.AltitudeM, &it.DepthM, &it.HorizontalLengthM, &it.GeologyID,
		&it.Geology,
	}
}

// ListPlaces is every place, by name: the data the table and the filters need.
func (r *Repository) ListPlaces(ctx context.Context) ([]ListItem, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT`+listColumns+listFrom+`
		ORDER BY lower(p.name), p.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []ListItem{}
	for rows.Next() {
		var it ListItem
		if err := rows.Scan(it.scanTargets()...); err != nil {
			return nil, err
		}
		list = append(list, it)
	}
	return list, rows.Err()
}

// GetPlace is a place with its points.
func (r *Repository) GetPlace(ctx context.Context, id int64) (Place, error) {
	var p Place
	targets := append(p.ListItem.scanTargets(), &p.Access, &p.Description, &p.TechnicalSheet,
		&p.CreatedBy, &p.CreatedAt, &p.UpdatedBy, &p.UpdatedAt)
	err := r.pool.QueryRow(ctx, `
		SELECT`+listColumns+`, p.access, p.description, p.technical_sheet,
			cu.login, p.created_at, uu.login, p.updated_at`+listFrom+`
		JOIN users cu ON cu.id = p.created_by
		JOIN users uu ON uu.id = p.updated_by
		WHERE p.id = $1`, id).Scan(targets...)
	if errors.Is(err, pgx.ErrNoRows) {
		return Place{}, ErrNotFound
	}
	if err != nil {
		return Place{}, err
	}
	if p.AreaPath, err = r.areaPath(ctx, p.AreaID); err != nil {
		return Place{}, err
	}
	if p.Points, err = r.ListPoints(ctx, id); err != nil {
		return Place{}, err
	}
	err = r.pool.QueryRow(ctx, `SELECT count(*) FROM place_files WHERE place_id = $1`, id).Scan(&p.FileCount)
	return p, err
}

// areaPath is a zone and the ones it is in, the country first.
func (r *Repository) areaPath(ctx context.Context, areaID int64) ([]AreaRef, error) {
	rows, err := r.pool.Query(ctx, `
		WITH RECURSIVE up AS (
			SELECT a.id, a.parent_id, a.kind, a.name, 0 AS depth FROM areas a WHERE a.id = $1
			UNION ALL
			SELECT a.id, a.parent_id, a.kind, a.name, up.depth + 1 FROM areas a JOIN up ON a.id = up.parent_id
		)
		SELECT id, kind, name FROM up ORDER BY depth DESC`, areaID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	path := []AreaRef{}
	for rows.Next() {
		var ref AreaRef
		if err := rows.Scan(&ref.ID, &ref.Kind, &ref.Name); err != nil {
			return nil, err
		}
		path = append(path, ref)
	}
	return path, rows.Err()
}

// AreaExists says whether a zone exists.
func (r *Repository) AreaExists(ctx context.Context, id int64) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM areas WHERE id = $1)`, id).Scan(&exists)
	return exists, err
}

func (r *Repository) CreatePlace(ctx context.Context, userID int64, kind Kind, in Input) (Place, error) {
	var id int64
	err := r.pool.QueryRow(ctx, `
		INSERT INTO places (type_id, kind, name, alt_names, state, area_id, lat, lon, altitude_m, access,
			description, depth_m, horizontal_length_m, geology_id, technical_sheet, created_by, updated_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $16)
		RETURNING id
	`, in.TypeID, kind, in.Name, in.AltNames, in.State, in.AreaID, *in.Lat, *in.Lon, in.AltitudeM, in.Access,
		in.Description, in.DepthM, in.HorizontalLengthM, in.GeologyID, in.TechnicalSheet, userID).Scan(&id)
	if isForeignKeyViolation(err) {
		return Place{}, ErrUnknownType
	}
	if err != nil {
		return Place{}, err
	}
	return r.GetPlace(ctx, id)
}

// UpdatePlace replaces every field of a place (and who changed it, and when).
func (r *Repository) UpdatePlace(ctx context.Context, id, userID int64, kind Kind, in Input) (Place, error) {
	tag, err := r.pool.Exec(ctx, `
		UPDATE places SET type_id = $2, kind = $3, name = $4, alt_names = $5, state = $6, area_id = $7,
			lat = $8, lon = $9, altitude_m = $10, access = $11, description = $12, depth_m = $13,
			horizontal_length_m = $14, geology_id = $15, technical_sheet = $16, updated_by = $17, updated_at = now()
		WHERE id = $1
	`, id, in.TypeID, kind, in.Name, in.AltNames, in.State, in.AreaID, *in.Lat, *in.Lon, in.AltitudeM, in.Access,
		in.Description, in.DepthM, in.HorizontalLengthM, in.GeologyID, in.TechnicalSheet, userID)
	if isForeignKeyViolation(err) {
		return Place{}, ErrUnknownType
	}
	if err != nil {
		return Place{}, err
	}
	if tag.RowsAffected() == 0 {
		return Place{}, ErrNotFound
	}
	return r.GetPlace(ctx, id)
}

// DeletePlace removes a place with its points and its files, in one transaction: nothing cascades in
// the database. It answers with the ids of the files, so that the caller can remove their bytes.
func (r *Repository) DeletePlace(ctx context.Context, id int64) ([]int64, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err := lockPlace(ctx, tx, id); err != nil {
		return nil, err
	}
	var activities int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM activity_places WHERE place_id = $1`, id).Scan(&activities); err != nil {
		return nil, err
	}
	if activities > 0 {
		return nil, ErrHasActivities
	}
	if _, err := tx.Exec(ctx, `DELETE FROM place_points WHERE place_id = $1`, id); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `DELETE FROM place_files WHERE place_id = $1 RETURNING id`, id)
	if err != nil {
		return nil, err
	}
	var files []int64
	for rows.Next() {
		var fileID int64
		if err := rows.Scan(&fileID); err != nil {
			rows.Close()
			return nil, err
		}
		files = append(files, fileID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM places WHERE id = $1`, id); err != nil {
		return nil, err
	}
	return files, tx.Commit(ctx)
}

// lockPlace locks the row of a place for the rest of the transaction, or says it is not there.
func lockPlace(ctx context.Context, tx pgx.Tx, id int64) error {
	var locked int64
	err := tx.QueryRow(ctx, `SELECT id FROM places WHERE id = $1 FOR UPDATE`, id).Scan(&locked)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// ----- points -----

const pointColumns = `id, place_id, kind, name, lat, lon, altitude_m, note, created_at`

func scanPoint(row pgx.Row, p *Point) error {
	return row.Scan(&p.ID, &p.PlaceID, &p.Kind, &p.Name, &p.Lat, &p.Lon, &p.AltitudeM, &p.Note, &p.CreatedAt)
}

// ListPoints is the other points of a place, in the order they were added.
func (r *Repository) ListPoints(ctx context.Context, placeID int64) ([]Point, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+pointColumns+` FROM place_points WHERE place_id = $1 ORDER BY id`, placeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []Point{}
	for rows.Next() {
		var p Point
		if err := scanPoint(rows, &p); err != nil {
			return nil, err
		}
		list = append(list, p)
	}
	return list, rows.Err()
}

func (r *Repository) GetPoint(ctx context.Context, placeID, id int64) (Point, error) {
	var p Point
	err := scanPoint(r.pool.QueryRow(ctx, `SELECT `+pointColumns+` FROM place_points WHERE place_id = $1 AND id = $2`, placeID, id), &p)
	if errors.Is(err, pgx.ErrNoRows) {
		return Point{}, ErrPointNotFound
	}
	return p, err
}

// touch records that somebody changed the place (its points are part of it).
func touch(ctx context.Context, tx pgx.Tx, placeID, userID int64) error {
	_, err := tx.Exec(ctx, `UPDATE places SET updated_by = $2, updated_at = now() WHERE id = $1`, placeID, userID)
	return err
}

func (r *Repository) AddPoint(ctx context.Context, placeID, userID int64, in PointInput) (Point, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Point{}, err
	}
	defer tx.Rollback(ctx)
	if err := lockPlace(ctx, tx, placeID); err != nil {
		return Point{}, err
	}
	var p Point
	err = scanPoint(tx.QueryRow(ctx, `
		INSERT INTO place_points (place_id, kind, name, lat, lon, altitude_m, note) VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING `+pointColumns, placeID, in.Kind, in.Name, *in.Lat, *in.Lon, in.AltitudeM, in.Note), &p)
	if err != nil {
		return Point{}, err
	}
	if err := touch(ctx, tx, placeID, userID); err != nil {
		return Point{}, err
	}
	return p, tx.Commit(ctx)
}

func (r *Repository) UpdatePoint(ctx context.Context, placeID, id, userID int64, in PointInput) (Point, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Point{}, err
	}
	defer tx.Rollback(ctx)
	if err := lockPlace(ctx, tx, placeID); err != nil {
		return Point{}, err
	}
	var p Point
	err = scanPoint(tx.QueryRow(ctx, `
		UPDATE place_points SET kind = $3, name = $4, lat = $5, lon = $6, altitude_m = $7, note = $8
		WHERE place_id = $1 AND id = $2
		RETURNING `+pointColumns, placeID, id, in.Kind, in.Name, *in.Lat, *in.Lon, in.AltitudeM, in.Note), &p)
	if errors.Is(err, pgx.ErrNoRows) {
		return Point{}, ErrPointNotFound
	}
	if err != nil {
		return Point{}, err
	}
	if err := touch(ctx, tx, placeID, userID); err != nil {
		return Point{}, err
	}
	return p, tx.Commit(ctx)
}

func (r *Repository) DeletePoint(ctx context.Context, placeID, id, userID int64) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := lockPlace(ctx, tx, placeID); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `DELETE FROM place_points WHERE place_id = $1 AND id = $2`, placeID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrPointNotFound
	}
	if err := touch(ctx, tx, placeID, userID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ----- types -----

const typeSelect = `
	SELECT t.id, t.name, t.kind, (SELECT count(*) FROM places p WHERE p.type_id = t.id)
	FROM place_types t`

// ListTypes is every type with how many places have it, in the order they were added.
func (r *Repository) ListTypes(ctx context.Context) ([]Type, error) {
	rows, err := r.pool.Query(ctx, typeSelect+` ORDER BY t.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []Type{}
	for rows.Next() {
		var t Type
		if err := rows.Scan(&t.ID, &t.Name, &t.Kind, &t.PlaceCount); err != nil {
			return nil, err
		}
		list = append(list, t)
	}
	return list, rows.Err()
}

func (r *Repository) GetType(ctx context.Context, id int64) (Type, error) {
	var t Type
	err := r.pool.QueryRow(ctx, typeSelect+` WHERE t.id = $1`, id).Scan(&t.ID, &t.Name, &t.Kind, &t.PlaceCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return Type{}, ErrTypeNotFound
	}
	return t, err
}

// TypeNamed is the type with that name, ignoring case, or ErrTypeNotFound.
func (r *Repository) TypeNamed(ctx context.Context, name string) (Type, error) {
	var t Type
	err := r.pool.QueryRow(ctx, typeSelect+` WHERE lower(t.name) = lower($1)`, name).Scan(&t.ID, &t.Name, &t.Kind, &t.PlaceCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return Type{}, ErrTypeNotFound
	}
	return t, err
}

// CreateType adds a general type: the types of the other kinds are fixed.
func (r *Repository) CreateType(ctx context.Context, name string) (Type, error) {
	var id int64
	err := r.pool.QueryRow(ctx, `INSERT INTO place_types (name, kind) VALUES ($1, 'general') RETURNING id`, name).Scan(&id)
	if isUniqueViolation(err) {
		return Type{}, ErrDuplicateType
	}
	if err != nil {
		return Type{}, err
	}
	return r.GetType(ctx, id)
}

func (r *Repository) RenameType(ctx context.Context, id int64, name string) (Type, error) {
	tag, err := r.pool.Exec(ctx, `UPDATE place_types SET name = $2 WHERE id = $1`, id, name)
	if isUniqueViolation(err) {
		return Type{}, ErrDuplicateType
	}
	if err != nil {
		return Type{}, err
	}
	if tag.RowsAffected() == 0 {
		return Type{}, ErrTypeNotFound
	}
	return r.GetType(ctx, id)
}

// DeleteType removes a type no place has: the foreign key refuses otherwise.
func (r *Repository) DeleteType(ctx context.Context, id int64) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM place_types WHERE id = $1`, id)
	if isForeignKeyViolation(err) {
		return ErrTypeInUse
	}
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrTypeNotFound
	}
	return nil
}

// ----- geologies -----

const geologySelect = `
	SELECT g.id, g.name, (SELECT count(*) FROM places p WHERE p.geology_id = g.id)
	FROM place_geologies g`

// ListGeologies is every geology with how many places have it, in the order they were added.
func (r *Repository) ListGeologies(ctx context.Context) ([]Geology, error) {
	rows, err := r.pool.Query(ctx, geologySelect+` ORDER BY g.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []Geology{}
	for rows.Next() {
		var g Geology
		if err := rows.Scan(&g.ID, &g.Name, &g.PlaceCount); err != nil {
			return nil, err
		}
		list = append(list, g)
	}
	return list, rows.Err()
}

func (r *Repository) GetGeology(ctx context.Context, id int64) (Geology, error) {
	var g Geology
	err := r.pool.QueryRow(ctx, geologySelect+` WHERE g.id = $1`, id).Scan(&g.ID, &g.Name, &g.PlaceCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return Geology{}, ErrGeologyNotFound
	}
	return g, err
}

// GeologyNamed is the geology with that name, ignoring case, or ErrGeologyNotFound.
func (r *Repository) GeologyNamed(ctx context.Context, name string) (Geology, error) {
	var g Geology
	err := r.pool.QueryRow(ctx, geologySelect+` WHERE lower(g.name) = lower($1)`, name).Scan(&g.ID, &g.Name, &g.PlaceCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return Geology{}, ErrGeologyNotFound
	}
	return g, err
}

func (r *Repository) CreateGeology(ctx context.Context, name string) (Geology, error) {
	var id int64
	err := r.pool.QueryRow(ctx, `INSERT INTO place_geologies (name) VALUES ($1) RETURNING id`, name).Scan(&id)
	if isUniqueViolation(err) {
		return Geology{}, ErrDuplicateGeology
	}
	if err != nil {
		return Geology{}, err
	}
	return r.GetGeology(ctx, id)
}

func (r *Repository) RenameGeology(ctx context.Context, id int64, name string) (Geology, error) {
	tag, err := r.pool.Exec(ctx, `UPDATE place_geologies SET name = $2 WHERE id = $1`, id, name)
	if isUniqueViolation(err) {
		return Geology{}, ErrDuplicateGeology
	}
	if err != nil {
		return Geology{}, err
	}
	if tag.RowsAffected() == 0 {
		return Geology{}, ErrGeologyNotFound
	}
	return r.GetGeology(ctx, id)
}

// DeleteGeology removes a geology no place has: the foreign key refuses otherwise.
func (r *Repository) DeleteGeology(ctx context.Context, id int64) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM place_geologies WHERE id = $1`, id)
	if isForeignKeyViolation(err) {
		return ErrGeologyInUse
	}
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrGeologyNotFound
	}
	return nil
}
