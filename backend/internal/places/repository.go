package places

import (
	"context"
	"encoding/json"
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

// violates says whether the error is a violation of the named constraint.
func violates(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.ConstraintName == constraint
}

// ----- places -----

const listColumns = `
	p.id, p.type_id, t.name, p.kind, p.name, p.alt_names, COALESCE(p.state, ''), p.subtype_id, st.name, p.system_id, sy.name,
	p.area_id, w.ids, w.where_text,
	p.lat, p.lon, p.altitude_m, p.depth_m, p.horizontal_length_m, p.geology_id, g.name`

// listFrom is a place with its type and, through the tree of zones, the ids and the names of the zone it
// is in and of the ones it is in, from the most specific to the country (depth 0 is the zone itself).
const listFrom = `
	FROM places p
	JOIN place_types t ON t.id = p.type_id
	LEFT JOIN place_geologies g ON g.id = p.geology_id
	LEFT JOIN place_subtypes st ON st.id = p.subtype_id
	LEFT JOIN place_systems sy ON sy.id = p.system_id
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
		&it.ID, &it.TypeID, &it.TypeName, &it.Kind, &it.Name, &it.AltNames, &it.State, &it.SubtypeID,
		&it.Subtype, &it.SystemID, &it.System, &it.AreaID, &it.AreaIDs, &it.Where, &it.Lat, &it.Lon, &it.AltitudeM, &it.DepthM, &it.HorizontalLengthM, &it.GeologyID,
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
		if it.AltNames == nil {
			it.AltNames = []string{}
		}
		list = append(list, it)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	changed, err := r.osmChanges(ctx, nil)
	if err != nil {
		return nil, err
	}
	for i := range list {
		list[i].OsmChanged = len(changed[list[i].ID]) > 0
	}
	return list, nil
}

// GetPlace is a place with its points.
func (r *Repository) GetPlace(ctx context.Context, id int64) (Place, error) {
	var p Place
	targets := append(p.ListItem.scanTargets(), &p.Access, &p.Description, &p.TechnicalSheet,
		&p.OsmType, &p.OsmID, &p.CreatedBy, &p.CreatedAt, &p.UpdatedBy, &p.UpdatedAt)
	err := r.pool.QueryRow(ctx, `
		SELECT`+listColumns+`, p.access, p.description, p.technical_sheet, p.osm_type, p.osm_id,
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
	if p.AltNames == nil {
		p.AltNames = []string{}
	}
	if p.AreaPath, err = r.areaPath(ctx, p.AreaID); err != nil {
		return Place{}, err
	}
	if p.Points, err = r.ListPoints(ctx, id); err != nil {
		return Place{}, err
	}
	if p.OsmChanges, err = r.osmChangesOf(ctx, id); err != nil {
		return Place{}, err
	}
	p.OsmChanged = len(p.OsmChanges) > 0
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

// seenOf is the SQL that takes what an OpenStreetMap element is now, to keep as what the place has seen of it:
// its tags and its point, or nothing if the element is not named or not there.
func seenOf(osmType, osmID string) string {
	return `(SELECT jsonb_build_object('tags', e.tags, 'lat', e.lat, 'lon', e.lon) FROM osm_elements e
		WHERE e.osm_type = ` + osmType + ` AND e.osm_id = ` + osmID + `)`
}

func (r *Repository) CreatePlace(ctx context.Context, userID int64, t Type, in Input) (Place, error) {
	var id int64
	err := r.pool.QueryRow(ctx, `
		INSERT INTO places (type_id, kind, has_state, name, alt_names, state, subtype_id, system_id, area_id,
			lat, lon, altitude_m, access, description, depth_m, horizontal_length_m, geology_id,
			technical_sheet, osm_type, osm_id, osm_seen, created_by, updated_by)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18,
			$19, $20, `+seenOf("$19", "$20")+`, $21, $21)
		RETURNING id
	`, in.TypeID, t.Kind, t.HasState, in.Name, in.AltNames, in.State, in.SubtypeID, in.SystemID, in.AreaID,
		*in.Lat, *in.Lon, in.AltitudeM, in.Access, in.Description, in.DepthM, in.HorizontalLengthM,
		in.GeologyID, in.TechnicalSheet, in.OsmType, in.OsmID, userID).Scan(&id)
	if violates(err, "places_osm_element_fkey") {
		return Place{}, ErrUnknownOsmElement
	}
	if violates(err, "places_osm_element_key") {
		return Place{}, ErrOsmElementInUse
	}
	if isForeignKeyViolation(err) {
		return Place{}, ErrUnknownType
	}
	if err != nil {
		return Place{}, err
	}
	return r.GetPlace(ctx, id)
}

// UpdatePlace replaces every field of a place (and who changed it, and when).
func (r *Repository) UpdatePlace(ctx context.Context, id, userID int64, t Type, in Input) (Place, error) {
	tag, err := r.pool.Exec(ctx, `
		UPDATE places SET type_id = $2, kind = $3, has_state = $4, name = $5, alt_names = $6,
			state = NULLIF($7, ''), subtype_id = $8, system_id = $9, area_id = $10, lat = $11, lon = $12,
			altitude_m = $13, access = $14, description = $15, depth_m = $16, horizontal_length_m = $17,
			geology_id = $18, technical_sheet = $19, updated_by = $20, updated_at = now()
		WHERE id = $1
	`, id, in.TypeID, t.Kind, t.HasState, in.Name, in.AltNames, in.State, in.SubtypeID, in.SystemID,
		in.AreaID, *in.Lat, *in.Lon, in.AltitudeM, in.Access, in.Description, in.DepthM,
		in.HorizontalLengthM, in.GeologyID, in.TechnicalSheet, userID)
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
	SELECT t.id, t.name, t.kind, t.has_state, (SELECT count(*) FROM places p WHERE p.type_id = t.id)
	FROM place_types t`

// ListTypes is every type with how many places have it: the cavities, the canyons and then the general
// ones, each in the order they were added.
func (r *Repository) ListTypes(ctx context.Context) ([]Type, error) {
	rows, err := r.pool.Query(ctx, typeSelect+` ORDER BY CASE t.kind WHEN 'cave' THEN 0 WHEN 'canyon' THEN 1 ELSE 2 END, t.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []Type{}
	for rows.Next() {
		var t Type
		if err := rows.Scan(&t.ID, &t.Name, &t.Kind, &t.HasState, &t.PlaceCount); err != nil {
			return nil, err
		}
		list = append(list, t)
	}
	return list, rows.Err()
}

func (r *Repository) GetType(ctx context.Context, id int64) (Type, error) {
	var t Type
	err := r.pool.QueryRow(ctx, typeSelect+` WHERE t.id = $1`, id).Scan(&t.ID, &t.Name, &t.Kind, &t.HasState, &t.PlaceCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return Type{}, ErrTypeNotFound
	}
	return t, err
}

// TypeNamed is the type with that name, ignoring case, or ErrTypeNotFound.
func (r *Repository) TypeNamed(ctx context.Context, name string) (Type, error) {
	var t Type
	err := r.pool.QueryRow(ctx, typeSelect+` WHERE lower(t.name) = lower($1)`, name).Scan(&t.ID, &t.Name, &t.Kind, &t.HasState, &t.PlaceCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return Type{}, ErrTypeNotFound
	}
	return t, err
}

// CreateType adds a general type: the types of the other kinds are fixed. Whether its places have a
// state is said now and does not change.
func (r *Repository) CreateType(ctx context.Context, name string, hasState bool) (Type, error) {
	var id int64
	err := r.pool.QueryRow(ctx, `INSERT INTO place_types (name, kind, has_state) VALUES ($1, 'general', $2) RETURNING id`,
		name, hasState).Scan(&id)
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

// ----- subtypes -----

const subtypeSelect = `
	SELECT g.id, g.name, (SELECT count(*) FROM places p WHERE p.subtype_id = g.id)
	FROM place_subtypes g`

// ListSubtypes is every subtype with how many places have it, in the order they were added.
func (r *Repository) ListSubtypes(ctx context.Context) ([]Subtype, error) {
	rows, err := r.pool.Query(ctx, subtypeSelect+` ORDER BY g.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []Subtype{}
	for rows.Next() {
		var g Subtype
		if err := rows.Scan(&g.ID, &g.Name, &g.PlaceCount); err != nil {
			return nil, err
		}
		list = append(list, g)
	}
	return list, rows.Err()
}

func (r *Repository) GetSubtype(ctx context.Context, id int64) (Subtype, error) {
	var g Subtype
	err := r.pool.QueryRow(ctx, subtypeSelect+` WHERE g.id = $1`, id).Scan(&g.ID, &g.Name, &g.PlaceCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return Subtype{}, ErrSubtypeNotFound
	}
	return g, err
}

// SubtypeNamed is the subtype with that name, ignoring case, or ErrSubtypeNotFound.
func (r *Repository) SubtypeNamed(ctx context.Context, name string) (Subtype, error) {
	var g Subtype
	err := r.pool.QueryRow(ctx, subtypeSelect+` WHERE lower(g.name) = lower($1)`, name).Scan(&g.ID, &g.Name, &g.PlaceCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return Subtype{}, ErrSubtypeNotFound
	}
	return g, err
}

func (r *Repository) CreateSubtype(ctx context.Context, name string) (Subtype, error) {
	var id int64
	err := r.pool.QueryRow(ctx, `INSERT INTO place_subtypes (name) VALUES ($1) RETURNING id`, name).Scan(&id)
	if isUniqueViolation(err) {
		return Subtype{}, ErrDuplicateSubtype
	}
	if err != nil {
		return Subtype{}, err
	}
	return r.GetSubtype(ctx, id)
}

func (r *Repository) RenameSubtype(ctx context.Context, id int64, name string) (Subtype, error) {
	tag, err := r.pool.Exec(ctx, `UPDATE place_subtypes SET name = $2 WHERE id = $1`, id, name)
	if isUniqueViolation(err) {
		return Subtype{}, ErrDuplicateSubtype
	}
	if err != nil {
		return Subtype{}, err
	}
	if tag.RowsAffected() == 0 {
		return Subtype{}, ErrSubtypeNotFound
	}
	return r.GetSubtype(ctx, id)
}

// DeleteSubtype removes a subtype no place has: the foreign key refuses otherwise.
func (r *Repository) DeleteSubtype(ctx context.Context, id int64) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM place_subtypes WHERE id = $1`, id)
	if isForeignKeyViolation(err) {
		return ErrSubtypeInUse
	}
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrSubtypeNotFound
	}
	return nil
}

// SetOsm makes a place the element of the OpenStreetMap mirror it names, replacing the one it had (and who
// changed it, and when); an empty pair takes the link away.
func (r *Repository) SetOsm(ctx context.Context, id, userID int64, osmType *string, osmID *int64) (Place, error) {
	tag, err := r.pool.Exec(ctx, `
		UPDATE places SET osm_type = $2, osm_id = $3, osm_seen = `+seenOf("$2", "$3")+`, updated_by = $4,
			updated_at = now()
		WHERE id = $1
	`, id, osmType, osmID, userID)
	if violates(err, "places_osm_element_fkey") {
		return Place{}, ErrUnknownOsmElement
	}
	if violates(err, "places_osm_element_key") {
		return Place{}, ErrOsmElementInUse
	}
	if err != nil {
		return Place{}, err
	}
	if tag.RowsAffected() == 0 {
		return Place{}, ErrNotFound
	}
	return r.GetPlace(ctx, id)
}

// ReviewOsm takes what the element is now as what the place has seen of it, so that what changed in it no longer
// counts as a change to look at (and says nothing else of the place: who changed it last stays as it was).
func (r *Repository) ReviewOsm(ctx context.Context, id int64) (Place, error) {
	var linked bool
	err := r.pool.QueryRow(ctx, `SELECT osm_type IS NOT NULL FROM places WHERE id = $1`, id).Scan(&linked)
	if errors.Is(err, pgx.ErrNoRows) {
		return Place{}, ErrNotFound
	}
	if err != nil {
		return Place{}, err
	}
	if !linked {
		return Place{}, ErrNotLinked
	}
	if _, err := r.pool.Exec(ctx, `UPDATE places SET osm_seen = `+seenOf("places.osm_type", "places.osm_id")+` WHERE id = $1`, id); err != nil {
		return Place{}, err
	}
	return r.GetPlace(ctx, id)
}

// osmChangesOf is what is different in the element of a place now from what it was when it was last seen.
func (r *Repository) osmChangesOf(ctx context.Context, id int64) ([]OsmChange, error) {
	list, err := r.osmChanges(ctx, &id)
	return list[id], err
}

// osmChanges is the changes of the places that are an element of OpenStreetMap (one place if id is given, all
// of them if not), by place.
func (r *Repository) osmChanges(ctx context.Context, id *int64) (map[int64][]OsmChange, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT p.id, p.osm_seen, e.tags, e.lat, e.lon, e.deleted_at IS NOT NULL
		FROM places p JOIN osm_elements e ON e.osm_type = p.osm_type AND e.osm_id = p.osm_id
		WHERE p.osm_seen IS NOT NULL AND ($1::bigint IS NULL OR p.id = $1)
	`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][]OsmChange{}
	for rows.Next() {
		var placeID int64
		var seen, tags []byte
		var now osmSnapshot
		var gone bool
		if err := rows.Scan(&placeID, &seen, &tags, &now.Lat, &now.Lon, &gone); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(tags, &now.Tags); err != nil {
			return nil, err
		}
		if changes := osmChanges(seen, now, gone); len(changes) > 0 {
			out[placeID] = changes
		}
	}
	return out, rows.Err()
}
