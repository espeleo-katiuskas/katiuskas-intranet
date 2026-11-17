package osmmirror_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/osmmirror"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/testutil"
)

func cave(id int64, name string) osmmirror.Element {
	return osmmirror.Element{
		Type: "node", ID: id, Version: 1, Timestamp: time.Unix(1700000000, 0).UTC(), Category: "cave",
		Lat: 40.8, Lon: -3.6, Tags: map[string]string{"natural": "cave_entrance", "name": name},
	}
}

func hut(id int64) osmmirror.Element {
	return osmmirror.Element{
		Type: "way", ID: id, Version: 2, Timestamp: time.Unix(1700000000, 0).UTC(), Category: "hut",
		Lat: 40.1, Lon: -1.9, Tags: map[string]string{"tourism": "wilderness_hut"},
		Geometry: map[string]any{"type": "Polygon", "coordinates": []any{[]any{
			[]any{-2.0, 40.0}, []any{-2.0, 40.2}, []any{-1.8, 40.2}, []any{-2.0, 40.0}}}},
	}
}

type row struct {
	name    string
	deleted bool
	source  string
}

func rows(t *testing.T, tx pgx.Tx) map[osmmirror.Key]row {
	t.Helper()
	r, err := tx.Query(context.Background(),
		`SELECT osm_type, osm_id, coalesce(tags->>'name', ''), deleted_at IS NOT NULL, source FROM osm_elements`)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	out := map[osmmirror.Key]row{}
	for r.Next() {
		var k osmmirror.Key
		var v row
		if err := r.Scan(&k.Type, &k.ID, &v.name, &v.deleted, &v.source); err != nil {
			t.Fatal(err)
		}
		out[k] = v
	}
	return out
}

// A load adds what is new, only confirms what is the same, updates what changed (even when the version did
// not), marks what is gone and unmarks what comes back; it never touches the elements of another source.
func TestApply(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := osmmirror.NewRepository(tx)
	ctx := context.Background()

	res, err := repo.Apply(ctx, "spain", []osmmirror.Element{cave(1, "A"), cave(2, "B"), hut(3)})
	if err != nil || res != (osmmirror.Result{Added: 3}) {
		t.Fatalf("first load: %+v %v", res, err)
	}
	if _, err := repo.Apply(ctx, "canary-islands", []osmmirror.Element{cave(10, "C")}); err != nil {
		t.Fatal(err)
	}

	// The same again: nothing changes.
	res, err = repo.Apply(ctx, "spain", []osmmirror.Element{cave(1, "A"), cave(2, "B"), hut(3)})
	if err != nil || res != (osmmirror.Result{Unchanged: 3}) {
		t.Fatalf("same load: %+v %v", res, err)
	}

	// 1 is renamed with no new version, 2 is gone, 4 is new.
	res, err = repo.Apply(ctx, "spain", []osmmirror.Element{cave(1, "A2"), hut(3), cave(4, "D")})
	if err != nil || res != (osmmirror.Result{Added: 1, Changed: 1, Deleted: 1, Unchanged: 1}) {
		t.Fatalf("third load: %+v %v", res, err)
	}
	got := rows(t, tx)
	if got[osmmirror.Key{Type: "node", ID: 1}].name != "A2" || !got[osmmirror.Key{Type: "node", ID: 2}].deleted ||
		got[osmmirror.Key{Type: "node", ID: 4}].name != "D" {
		t.Errorf("unexpected rows: %+v", got)
	}
	if c := got[osmmirror.Key{Type: "node", ID: 10}]; c.deleted || c.source != "canary-islands" {
		t.Errorf("the elements of another source are not touched: %+v", c)
	}
	var geometry string
	if err := tx.QueryRow(ctx, `SELECT geometry->>'type' FROM osm_elements WHERE osm_type = 'way' AND osm_id = 3`).Scan(&geometry); err != nil || geometry != "Polygon" {
		t.Errorf("the outline of a way is kept: %q %v", geometry, err)
	}

	// 2 comes back: unmarked, and counted as changed.
	res, err = repo.Apply(ctx, "spain", []osmmirror.Element{cave(1, "A2"), cave(2, "B"), hut(3), cave(4, "D")})
	if err != nil || res != (osmmirror.Result{Changed: 1, Unchanged: 3}) {
		t.Fatalf("fourth load: %+v %v", res, err)
	}
	if rows(t, tx)[osmmirror.Key{Type: "node", ID: 2}].deleted {
		t.Error("an element that comes back is no longer marked as gone")
	}
	if n, err := repo.Active(ctx, "spain"); err != nil || n != 4 {
		t.Errorf("4 active elements expected: %d %v", n, err)
	}
}

// The hash tells the same element from a changed one, whatever the order of the tags.
func TestContentHash(t *testing.T) {
	a, b := cave(1, "A"), cave(1, "A")
	if osmmirror.ContentHash(a) != osmmirror.ContentHash(b) {
		t.Error("the same element must give the same hash")
	}
	b.Lat += 0.0000001
	if osmmirror.ContentHash(a) == osmmirror.ContentHash(b) {
		t.Error("a moved point must change the hash")
	}
	c := hut(3)
	d := hut(3)
	d.Geometry["type"] = "MultiPolygon"
	if osmmirror.ContentHash(c) == osmmirror.ContentHash(d) {
		t.Error("another outline must change the hash")
	}
}

// A place may point at an element of the mirror, at most one place per element, and an element a place
// points at cannot be removed.
func TestPlacePointsAtElement(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := osmmirror.NewRepository(tx)
	ctx := context.Background()
	if _, err := repo.Apply(ctx, "spain", []osmmirror.Element{cave(1, "A")}); err != nil {
		t.Fatal(err)
	}
	user := testutil.InsertMember(t, tx)
	var typeID int64
	if err := tx.QueryRow(ctx, `SELECT id FROM place_types WHERE name = 'Cavidad'`).Scan(&typeID); err != nil {
		t.Fatal(err)
	}
	var subtypeID, areaID int64
	if err := tx.QueryRow(ctx, `SELECT id FROM place_subtypes ORDER BY id LIMIT 1`).Scan(&subtypeID); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, `SELECT id FROM areas WHERE parent_id IS NULL ORDER BY id LIMIT 1`).Scan(&areaID); err != nil {
		t.Fatal(err)
	}
	insert := func(osmType any, osmID any) error {
		sp, err := tx.Begin(ctx) // A savepoint: a failed statement must not end the transaction.
		if err != nil {
			t.Fatal(err)
		}
		_, err = sp.Exec(ctx, `
			INSERT INTO places (type_id, kind, subtype_id, has_state, state, name, area_id, lat, lon, created_by,
				updated_by, osm_type, osm_id)
			VALUES ($1, 'cave', $2, true, 'open', 'Una cueva', $3, 40.8, -3.6, $4, $4, $5, $6)
		`, typeID, subtypeID, areaID, user.ID, osmType, osmID)
		if err != nil {
			_ = sp.Rollback(ctx)
			return err
		}
		return sp.Commit(ctx)
	}
	if err := insert("node", int64(1)); err != nil {
		t.Fatalf("a place that is an element of the mirror: %v", err)
	}
	if err := insert("node", int64(1)); err == nil {
		t.Error("a second place for the same element must be refused")
	}
	if err := insert("node", nil); err == nil {
		t.Error("a type without an id must be refused")
	}
	if err := insert("node", int64(99)); err == nil {
		t.Error("an element that is not in the mirror must be refused")
	}
	if err := insert(nil, nil); err != nil {
		t.Errorf("a place that is not an element of OpenStreetMap: %v", err)
	}
}

// fakeExtractor writes the elements it is told to as the file the loader reads.
type fakeExtractor struct {
	dir      string
	stamp    time.Time
	elements string
	routes   string
}

func (f *fakeExtractor) Timestamp(context.Context, string) (time.Time, error) { return f.stamp, nil }

func (f *fakeExtractor) Export(context.Context, string) (string, func(), error) {
	path := filepath.Join(f.dir, "e.geojsonseq")
	return path, func() {}, os.WriteFile(path, []byte(f.elements), 0o600)
}

func (f *fakeExtractor) ExportRoutes(context.Context, string) (string, func(), error) {
	path := filepath.Join(f.dir, "routes.opl")
	return path, func() {}, os.WriteFile(path, []byte(f.routes), 0o600)
}

func elementsOf(n int) string {
	var lines []string
	for i := 1; i <= n; i++ {
		lines = append(lines, feature(point, `{"@type":"node","@id":`+itoa(i)+`,"@version":1,"@timestamp":1700000000,"natural":"cave_entrance","name":"C"}`))
	}
	return seq(lines...)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var d []byte
	for ; i > 0; i /= 10 {
		d = append([]byte{byte('0' + i%10)}, d...)
	}
	return string(d)
}

// The syncer logs each load, does nothing when the extract is the one already loaded, and refuses an
// extract that has lost most of what the source had.
func TestSyncer(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := osmmirror.NewRepository(tx)
	ctx := context.Background()
	ex := &fakeExtractor{dir: t.TempDir(), stamp: time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC), elements: elementsOf(30)}
	s := osmmirror.NewSyncer(repo, ex)

	res, err := s.Sync(ctx, "spain", "x.osm.pbf", false)
	if err != nil || res.Added != 30 {
		t.Fatalf("first load: %+v %v", res, err)
	}
	last, err := repo.LastOK(ctx, "spain")
	if err != nil || last == nil || last.Added != 30 || last.Status != "ok" || last.FinishedAt == nil ||
		last.SourceTimestamp == nil || !last.SourceTimestamp.Equal(ex.stamp) {
		t.Fatalf("the log of the first load: %+v %v", last, err)
	}

	// The same extract: nothing to do.
	if _, err := s.Sync(ctx, "spain", "x.osm.pbf", false); !errors.Is(err, osmmirror.ErrUpToDate) {
		t.Errorf("expected ErrUpToDate, got %v", err)
	}

	// A newer extract with almost nothing in it: refused, nothing is marked as gone, and the log says why.
	ex.stamp = ex.stamp.Add(24 * time.Hour)
	ex.elements = elementsOf(3)
	if _, err := s.Sync(ctx, "spain", "x.osm.pbf", false); !errors.Is(err, osmmirror.ErrTooFew) {
		t.Fatalf("expected ErrTooFew, got %v", err)
	}
	if n, _ := repo.Active(ctx, "spain"); n != 30 {
		t.Errorf("nothing may be touched by a refused load, %d active", n)
	}
	var status, msg string
	if err := tx.QueryRow(ctx, `SELECT status, coalesce(error, '') FROM osm_sync_runs ORDER BY id DESC LIMIT 1`).Scan(&status, &msg); err != nil || status != "failed" || msg == "" {
		t.Errorf("the failed run is logged: %q %q %v", status, msg, err)
	}
	// With force it is accepted.
	res, err = s.Sync(ctx, "spain", "x.osm.pbf", true)
	if err != nil || res.Deleted != 27 || res.Unchanged != 3 {
		t.Errorf("forced load: %+v %v", res, err)
	}
	// Another source has its own history.
	if _, err := s.Sync(ctx, "canary-islands", "y.osm.pbf", false); err != nil {
		t.Errorf("another source loads on its own: %v", err)
	}
}
