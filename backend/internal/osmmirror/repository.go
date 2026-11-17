package osmmirror

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/platform/db"
)

type Repository struct {
	pool db.PGX
}

func NewRepository(pool db.PGX) *Repository { return &Repository{pool: pool} }

// Result counts what a load did.
type Result struct {
	Added, Changed, Deleted, Unchanged int
}

// ContentHash is a hash of what an element is as far as the mirror is concerned: its tags, its point and
// its outline. Two loads of the same element give the same hash, and any change gives another, including a
// node of a way that moved (which does not change the way's version).
func ContentHash(e Element) string {
	body, _ := json.Marshal(struct { // Maps are written with their keys in order.
		Tags     map[string]string `json:"tags"`
		Lat, Lon float64
		Geometry map[string]any `json:"geometry"`
	}{e.Tags, e.Lat, e.Lon, e.Geometry})
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

type stored struct {
	hash    string
	version int
	deleted bool
	source  string
}

func (r *Repository) stored(ctx context.Context, tx pgx.Tx) (map[Key]stored, error) {
	rows, err := tx.Query(ctx, `SELECT osm_type, osm_id, content_hash, version, deleted_at IS NOT NULL, source FROM osm_elements`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[Key]stored{}
	for rows.Next() {
		var k Key
		var s stored
		if err := rows.Scan(&k.Type, &k.ID, &s.hash, &s.version, &s.deleted, &s.source); err != nil {
			return nil, err
		}
		out[k] = s
	}
	return out, rows.Err()
}

// Active is how many elements of a source are in the mirror and not marked as gone.
func (r *Repository) Active(ctx context.Context, source string) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx, `SELECT count(*) FROM osm_elements WHERE source = $1 AND deleted_at IS NULL`, source).Scan(&n)
	return n, err
}

// batchSize is how many elements go in one statement.
const batchSize = 2000

// Apply makes the mirror say what the extract `source` says, in one transaction: what is new is added, what
// changed (another hash) is updated, what is the same is only confirmed, and what the source had before and
// no longer has is marked as gone (and unmarked if it comes back). Running it twice with the same elements
// changes nothing but the time of the confirmation.
func (r *Repository) Apply(ctx context.Context, source string, elements []Element) (Result, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Result{}, err
	}
	defer tx.Rollback(ctx)
	have, err := r.stored(ctx, tx)
	if err != nil {
		return Result{}, err
	}
	var res Result
	var write, confirm []Element
	seen := make(map[Key]bool, len(elements))
	for _, e := range elements {
		seen[e.Key()] = true
		old, ok := have[e.Key()]
		switch {
		case !ok:
			res.Added++
			write = append(write, e)
		case old.hash != ContentHash(e) || old.version != e.Version || old.deleted || old.source != source:
			res.Changed++
			write = append(write, e)
		default:
			res.Unchanged++
			confirm = append(confirm, e)
		}
	}
	for start := 0; start < len(write); start += batchSize {
		if err := upsert(ctx, tx, source, write[start:min(start+batchSize, len(write))]); err != nil {
			return Result{}, err
		}
	}
	for start := 0; start < len(confirm); start += batchSize {
		if err := touch(ctx, tx, confirm[start:min(start+batchSize, len(confirm))]); err != nil {
			return Result{}, err
		}
	}
	var gone []Element
	for k, s := range have {
		if s.source == source && !s.deleted && !seen[k] {
			gone = append(gone, Element{Type: k.Type, ID: k.ID})
		}
	}
	res.Deleted = len(gone)
	for start := 0; start < len(gone); start += batchSize {
		if err := markGone(ctx, tx, gone[start:min(start+batchSize, len(gone))]); err != nil {
			return Result{}, err
		}
	}
	return res, tx.Commit(ctx)
}

func upsert(ctx context.Context, tx pgx.Tx, source string, list []Element) error {
	n := len(list)
	types, ids, versions := make([]string, n), make([]int64, n), make([]int32, n)
	stamps, cats := make([]time.Time, n), make([]string, n)
	lats, lons := make([]float64, n), make([]float64, n)
	tags, geoms, hashes := make([]string, n), make([]*string, n), make([]string, n)
	for i, e := range list {
		types[i], ids[i], versions[i], stamps[i], cats[i] = e.Type, e.ID, int32(e.Version), e.Timestamp, e.Category
		lats[i], lons[i] = e.Lat, e.Lon
		t, err := json.Marshal(e.Tags)
		if err != nil {
			return err
		}
		tags[i] = string(t)
		if e.Geometry != nil {
			g, err := json.Marshal(e.Geometry)
			if err != nil {
				return err
			}
			s := string(g)
			geoms[i] = &s
		}
		hashes[i] = ContentHash(e)
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO osm_elements (osm_type, osm_id, version, osm_timestamp, category, lat, lon, tags, geometry,
			content_hash, source)
		SELECT t, i, v, ts, c, la, lo, tg::jsonb, g::jsonb, h, $11
		FROM unnest($1::text[], $2::bigint[], $3::int[], $4::timestamptz[], $5::text[], $6::float8[], $7::float8[],
			$8::text[], $9::text[], $10::text[]) AS u(t, i, v, ts, c, la, lo, tg, g, h)
		ON CONFLICT (osm_type, osm_id) DO UPDATE SET
			version = EXCLUDED.version, osm_timestamp = EXCLUDED.osm_timestamp, category = EXCLUDED.category,
			lat = EXCLUDED.lat, lon = EXCLUDED.lon, tags = EXCLUDED.tags, geometry = EXCLUDED.geometry,
			content_hash = EXCLUDED.content_hash, source = EXCLUDED.source, fetched_at = now(),
			changed_at = now(), deleted_at = NULL
	`, types, ids, versions, stamps, cats, lats, lons, tags, geoms, hashes, source)
	return err
}

func keysOf(list []Element) (types []string, ids []int64) {
	for _, e := range list {
		types = append(types, e.Type)
		ids = append(ids, e.ID)
	}
	return types, ids
}

// touch records that a load confirmed elements that did not change.
func touch(ctx context.Context, tx pgx.Tx, list []Element) error {
	types, ids := keysOf(list)
	_, err := tx.Exec(ctx, `
		UPDATE osm_elements o SET fetched_at = now()
		FROM unnest($1::text[], $2::bigint[]) AS u(t, i) WHERE o.osm_type = u.t AND o.osm_id = u.i
	`, types, ids)
	return err
}

func markGone(ctx context.Context, tx pgx.Tx, list []Element) error {
	types, ids := keysOf(list)
	_, err := tx.Exec(ctx, `
		UPDATE osm_elements o SET deleted_at = now(), changed_at = now()
		FROM unnest($1::text[], $2::bigint[]) AS u(t, i) WHERE o.osm_type = u.t AND o.osm_id = u.i
	`, types, ids)
	return err
}

// ----- the log of loads -----

// Run is a row of the log.
type Run struct {
	ID              int64
	Source          string
	SourceTimestamp *time.Time
	StartedAt       time.Time
	FinishedAt      *time.Time
	Status          string
	Result
	Error *string
}

// StartRun writes the row of a load that begins.
func (r *Repository) StartRun(ctx context.Context, source string, sourceTimestamp time.Time) (int64, error) {
	var id int64
	err := r.pool.QueryRow(ctx, `INSERT INTO osm_sync_runs (source, source_timestamp) VALUES ($1, $2) RETURNING id`,
		source, sourceTimestamp).Scan(&id)
	return id, err
}

// FinishRun closes the row: ok with what was done, or failed with why.
func (r *Repository) FinishRun(ctx context.Context, id int64, res Result, runErr error) error {
	status, msg := "ok", (*string)(nil)
	if runErr != nil {
		s := runErr.Error()
		status, msg = "failed", &s
	}
	_, err := r.pool.Exec(ctx, `
		UPDATE osm_sync_runs SET finished_at = now(), status = $2, added = $3, changed = $4, deleted = $5,
			unchanged = $6, error = $7 WHERE id = $1
	`, id, status, res.Added, res.Changed, res.Deleted, res.Unchanged, msg)
	return err
}

// LastOK is the latest successful run of a source, or nil if there is none.
func (r *Repository) LastOK(ctx context.Context, source string) (*Run, error) {
	var run Run
	err := r.pool.QueryRow(ctx, `
		SELECT id, source, source_timestamp, started_at, finished_at, status, added, changed, deleted, unchanged, error
		FROM osm_sync_runs WHERE source = $1 AND status = 'ok' ORDER BY started_at DESC, id DESC LIMIT 1
	`, source).Scan(&run.ID, &run.Source, &run.SourceTimestamp, &run.StartedAt, &run.FinishedAt, &run.Status,
		&run.Added, &run.Changed, &run.Deleted, &run.Unchanged, &run.Error)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("last successful run: %w", err)
	}
	return &run, nil
}

// ----- reading, for the maps -----

// ErrNotFound means there is no such element in the mirror.
var ErrNotFound = errors.New("no such OpenStreetMap element")

// Bounds is a box of latitudes and longitudes. When West is greater than East the box goes across the
// antimeridian.
type Bounds struct {
	South, West, North, East float64
}

// Point is what a map needs to draw an element: where it is, what it is and its name.
type Point struct {
	Type     string  `json:"type"`
	ID       int64   `json:"id"`
	Lat      float64 `json:"lat"`
	Lon      float64 `json:"lon"`
	Category string  `json:"category"`
	Name     string  `json:"name,omitempty"`
}

// Detail is an element with everything the mirror knows of it.
type Detail struct {
	Type      string            `json:"type"`
	ID        int64             `json:"id"`
	Version   int               `json:"version"`
	Timestamp time.Time         `json:"timestamp"`
	Category  string            `json:"category"`
	Lat       float64           `json:"lat"`
	Lon       float64           `json:"lon"`
	Tags      map[string]string `json:"tags"`
	Geometry  json.RawMessage   `json:"geometry"`
	DeletedAt *time.Time        `json:"deleted_at"`
}

// InBounds is the elements that are not marked as gone inside a box and of the given categories, the ones
// of the club's own activity first (caves, then canyons, huts and via ferratas), at most limit of them;
// `more` says there were others. An element that already is a place of the catalogue is not given: it is
// on the map as that place.
func (r *Repository) InBounds(ctx context.Context, b Bounds, categories []string, limit int) (points []Point, more bool, err error) {
	lonTest := `lon BETWEEN $3 AND $4`
	if b.West > b.East {
		lonTest = `(lon >= $3 OR lon <= $4)`
	}
	rows, err := r.pool.Query(ctx, `
		SELECT osm_type, osm_id, lat, lon, category, coalesce(tags->>'name', '')
		FROM osm_elements e
		WHERE deleted_at IS NULL AND lat BETWEEN $1 AND $2 AND `+lonTest+` AND category = ANY($5)
			AND NOT EXISTS (SELECT 1 FROM places p WHERE p.osm_type = e.osm_type AND p.osm_id = e.osm_id)
		ORDER BY CASE category WHEN 'cave' THEN 0 WHEN 'canyon' THEN 1 WHEN 'hut' THEN 2 ELSE 3 END, osm_type, osm_id
		LIMIT $6
	`, b.South, b.North, b.West, b.East, categories, limit+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	points = []Point{}
	for rows.Next() {
		var p Point
		if err := rows.Scan(&p.Type, &p.ID, &p.Lat, &p.Lon, &p.Category, &p.Name); err != nil {
			return nil, false, err
		}
		points = append(points, p)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	if len(points) > limit {
		return points[:limit], true, nil
	}
	return points, false, nil
}

// Get is one element with all its data, marked as gone or not.
func (r *Repository) Get(ctx context.Context, typ string, id int64) (Detail, error) {
	var d Detail
	var tags []byte
	err := r.pool.QueryRow(ctx, `
		SELECT osm_type, osm_id, version, osm_timestamp, category, lat, lon, tags, geometry, deleted_at
		FROM osm_elements WHERE osm_type = $1 AND osm_id = $2
	`, typ, id).Scan(&d.Type, &d.ID, &d.Version, &d.Timestamp, &d.Category, &d.Lat, &d.Lon, &tags, &d.Geometry, &d.DeletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Detail{}, ErrNotFound
	}
	if err != nil {
		return Detail{}, err
	}
	if err := json.Unmarshal(tags, &d.Tags); err != nil {
		return Detail{}, err
	}
	return d, nil
}
