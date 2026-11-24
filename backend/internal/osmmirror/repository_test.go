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
		`SELECT osm_type, osm_id, coalesce(tags->>'name', ''), deleted_at IS NOT NULL, source FROM catalogue.osm_elements`)
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
	if err := tx.QueryRow(ctx, `SELECT geometry->>'type' FROM catalogue.osm_elements WHERE osm_type = 'way' AND osm_id = 3`).Scan(&geometry); err != nil || geometry != "Polygon" {
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
	if err := tx.QueryRow(ctx, `SELECT id FROM catalogue.place_types WHERE name = 'Cavidad'`).Scan(&typeID); err != nil {
		t.Fatal(err)
	}
	var subtypeID, areaID int64
	if err := tx.QueryRow(ctx, `SELECT id FROM catalogue.place_subtypes ORDER BY id LIMIT 1`).Scan(&subtypeID); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, `SELECT id FROM catalogue.areas WHERE parent_id IS NULL ORDER BY id LIMIT 1`).Scan(&areaID); err != nil {
		t.Fatal(err)
	}
	insert := func(osmType any, osmID any) error {
		sp, err := tx.Begin(ctx) // A savepoint: a failed statement must not end the transaction.
		if err != nil {
			t.Fatal(err)
		}
		_, err = sp.Exec(ctx, `
			INSERT INTO catalogue.places (type_id, kind, subtype_id, has_state, state, name, area_id, lat, lon, created_by,
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
	// boundaries are the polygons of the municipalities, as the geojsonseq of osmium.
	boundaries string
}

func (f *fakeExtractor) Timestamp(context.Context, string) (time.Time, error) { return f.stamp, nil }

func (f *fakeExtractor) Export(context.Context, string) (string, func(), error) {
	path := filepath.Join(f.dir, "e.geojsonseq")
	return path, func() {}, os.WriteFile(path, []byte(f.elements), 0o600)
}

func (f *fakeExtractor) ExportBoundaries(context.Context, string) (string, func(), error) {
	path := filepath.Join(f.dir, "boundaries.geojsonseq")
	return path, func() {}, os.WriteFile(path, []byte(f.boundaries), 0o600)
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
	if err := tx.QueryRow(ctx, `SELECT status, coalesce(error, '') FROM catalogue.osm_sync_runs ORDER BY id DESC LIMIT 1`).Scan(&status, &msg); err != nil || status != "failed" || msg == "" {
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

// A load works out the zone of each element from the boundaries of the extract and keeps it, and the search and
// the detail say where it is ("Valdemanco, Madrid") with the zones of the club; an element that is in no zone
// says nothing; and deleting the zone does not stop, it only empties the elements that were in it.
func TestSyncerWorksOutTheZone(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := osmmirror.NewRepository(tx)
	ctx := context.Background()
	var code string
	var zoneID int64
	if err := tx.QueryRow(ctx, `SELECT code, id FROM catalogue.areas WHERE kind = 'Municipio' AND name = 'Valdemanco'`).Scan(&code, &zoneID); err != nil {
		t.Fatal(err)
	}
	ex := &fakeExtractor{
		dir: t.TempDir(), stamp: time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC),
		elements: seq(
			feature(point, `{"@type":"node","@id":1,"@version":1,"@timestamp":1700000000,"natural":"cave_entrance","name":"Cueva del Agua"}`),
			feature(`{"type":"Point","coordinates":[10.0,50.0]}`, `{"@type":"node","@id":2,"@version":1,"@timestamp":1700000000,"natural":"cave_entrance","name":"Cueva del Agua"}`),
		),
		boundaries: boundary(`{"boundary":"administrative","admin_level":"8","ref:ine":"`+code+`000000"}`,
			`{"type":"Polygon","coordinates":`+square(-4, 40, -3, 41)+`}`) +
			// A municipality the club has no zone for: the sign that its list is behind.
			boundary(`{"boundary":"administrative","admin_level":"8","ref:ine":"99999000000"}`,
				`{"type":"Polygon","coordinates":`+square(20, 40, 21, 41)+`}`),
	}
	s := osmmirror.NewSyncer(repo, ex)
	if res, err := s.Sync(ctx, "spain", "x.osm.pbf", false); err != nil || res.Added != 2 {
		t.Fatalf("first load: %+v %v", res, err)
	}
	var unknown int
	var unknownCodes *string
	if err := tx.QueryRow(ctx, `SELECT unknown_zones, unknown_zone_codes FROM catalogue.osm_sync_runs
		WHERE source = 'spain' ORDER BY id DESC LIMIT 1`).Scan(&unknown, &unknownCodes); err != nil {
		t.Fatal(err)
	}
	if unknown != 1 || unknownCodes == nil || *unknownCodes != "99999" {
		t.Errorf("the run says one municipality is unknown, 99999: %d %v", unknown, unknownCodes)
	}
	var one, two *int64
	if err := tx.QueryRow(ctx, `SELECT (SELECT area_id FROM catalogue.osm_elements WHERE osm_id = 1), (SELECT area_id FROM catalogue.osm_elements WHERE osm_id = 2)`).Scan(&one, &two); err != nil {
		t.Fatal(err)
	}
	if one == nil || *one != zoneID || two != nil {
		t.Errorf("the first is in the zone and the second in none: %v %v", one, two)
	}

	service := osmmirror.NewService(repo)
	found, err := service.Search(ctx, "cueva del agua", nil)
	if err != nil || len(found) != 2 {
		t.Fatalf("both are found: %+v %v", found, err)
	}
	where := map[int64]string{}
	for _, p := range found {
		where[p.ID] = p.Where
	}
	if where[1] != "Valdemanco, Madrid" || where[2] != "" {
		t.Errorf("where each is: %+v", where)
	}
	d, err := repo.Get(ctx, "node", 1)
	if err != nil || d.Where != "Valdemanco, Madrid" || d.AreaID == nil || *d.AreaID != zoneID {
		t.Errorf("the detail says where, and in which zone: %q %v %v", d.Where, d.AreaID, err)
	}
	if d2, err := repo.Get(ctx, "node", 2); err != nil || d2.AreaID != nil || d2.Where != "" {
		t.Errorf("an element in no zone has none: %+v %v", d2.AreaID, err)
	}

	// The same extract again, a newer one: nothing changes, zones included.
	ex.stamp = ex.stamp.Add(24 * time.Hour)
	if res, err := s.Sync(ctx, "spain", "x.osm.pbf", false); err != nil || res.Changed != 0 || res.Unchanged != 2 {
		t.Errorf("same elements, same zones: %+v %v", res, err)
	}
	// Without the boundary the element loses its zone, which is a change.
	ex.stamp = ex.stamp.Add(24 * time.Hour)
	ex.boundaries = ""
	if res, err := s.Sync(ctx, "spain", "x.osm.pbf", false); err != nil || res.Changed != 1 {
		t.Errorf("an element that loses its zone changes: %+v %v", res, err)
	}
}

// The zone of an element is the one foreign key that is ON DELETE SET NULL: deleting the zone is not stopped by the
// elements in it, and they are left in none (the next load puts them back where they are).
func TestDeletingAZoneEmptiesTheElementsInIt(t *testing.T) {
	tx := testutil.NewTx(t)
	ctx := context.Background()
	// A country and a zone of its own, so that no other test is in the way; and an id of the element that no
	// other test uses, since the tests of several packages run at once on the same database.
	var country, zone int64
	if err := tx.QueryRow(ctx, `INSERT INTO catalogue.areas (kind, name, norm_name) VALUES ('País', 'Zonalandia', 'zonalandia') RETURNING id`).Scan(&country); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, `INSERT INTO catalogue.areas (parent_id, kind, name, norm_name) VALUES ($1, 'Provincia', 'Zona de prueba', 'zona de prueba') RETURNING id`, country).Scan(&zone); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO catalogue.osm_elements (osm_type, osm_id, version, osm_timestamp, category, lat, lon, tags, content_hash, source, area_id)
		VALUES ('node', 9000001, 1, now(), 'cave', 40, -3, '{}', 'h', 'spain', $1)`, zone); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM catalogue.areas WHERE id = $1`, zone); err != nil {
		t.Fatalf("a zone with elements in it can be deleted: %v", err)
	}
	var left *int64
	if err := tx.QueryRow(ctx, `SELECT area_id FROM catalogue.osm_elements WHERE osm_id = 9000001`).Scan(&left); err != nil || left != nil {
		t.Errorf("the element is in no zone now: %v %v", left, err)
	}
}

// The zones that a boundary of Spain can be: the municipalities by their INE code, and Ceuta and Melilla, which
// are autonomous cities with no municipality below them, by the code of their municipality.
func TestMunicipalityIDs(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := osmmirror.NewRepository(tx)
	ctx := context.Background()
	ids, err := repo.MunicipalityIDs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var valdemanco, ceuta, melilla int64
	if err := tx.QueryRow(ctx, `SELECT id FROM catalogue.areas WHERE kind = 'Municipio' AND name = 'Valdemanco'`).Scan(&valdemanco); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, `SELECT id FROM catalogue.areas WHERE kind = 'Ciudad autónoma' AND name = 'Ceuta'`).Scan(&ceuta); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, `SELECT id FROM catalogue.areas WHERE kind = 'Ciudad autónoma' AND name = 'Melilla'`).Scan(&melilla); err != nil {
		t.Fatal(err)
	}
	var code string
	if err := tx.QueryRow(ctx, `SELECT code FROM catalogue.areas WHERE id = $1`, valdemanco).Scan(&code); err != nil {
		t.Fatal(err)
	}
	if ids[code] != valdemanco || ids["51001"] != ceuta || ids["52001"] != melilla {
		t.Errorf("Valdemanco %d, Ceuta %d and Melilla %d expected: %d %d %d", valdemanco, ceuta, melilla, ids[code], ids["51001"], ids["52001"])
	}
	if len(ids) != 8130+2 {
		t.Errorf("8,130 municipalities and the 2 cities expected, got %d", len(ids))
	}
}
