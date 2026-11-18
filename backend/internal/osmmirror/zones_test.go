package osmmirror_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/osmmirror"
)

func boundary(props, geometry string) string {
	return "\x1e" + `{"type":"Feature","properties":` + props + `,"geometry":` + geometry + "}\n"
}

func square(x0, y0, x1, y1 float64) string {
	return fmt.Sprintf("[[[%g,%g],[%g,%g],[%g,%g],[%g,%g],[%g,%g]]]", x0, y0, x0, y1, x1, y1, x1, y0, x0, y0)
}

// The zone of each element is the one whose boundary holds its point: only boundaries of level 8 with an INE
// reference that is a zone of the club count (the first five digits of it), a hole is not inside, and an element
// in none has no zone.
func TestAssignZones(t *testing.T) {
	level8 := func(ref string) string {
		return `{"boundary":"administrative","admin_level":"8","ref:ine":"` + ref + `"}`
	}
	at := func(lat, lon float64) osmmirror.Element {
		return osmmirror.Element{Type: "node", ID: 1, Lat: lat, Lon: lon}
	}
	in := boundary(`{"boundary":"administrative","admin_level":"6","ref:ine":"28000000000"}`,
		`{"type":"Polygon","coordinates":`+square(-10, -10, 20, 20)+`}`) + // A province over everything: not a municipality.
		boundary(level8("28030000000"), `{"type":"Polygon","coordinates":`+square(0, 0, 2, 2)+`}`) +
		boundary(level8("28031000123"), `{"type":"Polygon","coordinates":`+square(2, 0, 4, 2)+`}`) + // The entity is cut off.
		boundary(level8("28032000000"), `{"type":"Polygon","coordinates":[[[10,10],[10,14],[14,14],[14,10],[10,10]],[[11,11],[11,13],[13,13],[13,11],[11,11]]]}`) +
		boundary(level8("28033000000"), `{"type":"MultiPolygon","coordinates":[`+square(5, 5, 6, 6)+`,`+square(7, 7, 8, 8)+`]}`) +
		boundary(`{"boundary":"administrative","admin_level":"8"}`, `{"type":"Polygon","coordinates":`+square(15, 15, 16, 16)+`}`) + // No reference.
		boundary(`{"place":"village","name":"Algo"}`, `{"type":"Polygon","coordinates":`+square(17, 17, 18, 18)+`}`) +
		boundary(level8("28034000000"), `{"type":"Polygon","coordinates":`+square(19, 19, 20, 20)+`}`) + // Not in the catalogue.
		// A Portuguese parish: its `ref:ine` has six digits, the first five of which are the code of a
		// Spanish municipality of the catalogue, and it is not that one.
		boundary(level8("280300"), `{"type":"Polygon","coordinates":`+square(21, 21, 22, 22)+`}`)
	elements := []osmmirror.Element{
		at(1, 1),       // in the first municipality
		at(1, 3),       // in the second, whose code is the five digits
		at(12, 12),     // in the hole of the third: in none (the province does not count)
		at(10.5, 10.5), // in the ring of the third
		at(7.5, 7.5),   // in the second part of the multipolygon
		at(5.5, 15.5),  // in the box of nothing
		at(15.5, 15.5), // in a boundary with no reference
		at(17.5, 17.5), // in a polygon that is not a boundary
		at(19.5, 19.5), // in a municipality that the club does not have
		at(21.5, 21.5), // in a Portuguese parish
	}
	// The zones of the club, by INE code; 28034 is on the map but not in the catalogue.
	resolve := osmmirror.MunicipalityResolver(map[string]int64{"28030": 30, "28031": 31, "28032": 32, "28033": 33})
	res, err := osmmirror.AssignZones(strings.NewReader(in), elements, osmmirror.ZoneRules{Municipality: resolve})
	if err != nil {
		t.Fatal(err)
	}
	want := []int64{30, 31, 0, 32, 33, 0, 0, 0, 0, 0}
	for i, w := range want {
		if elements[i].AreaID != w {
			t.Errorf("element %d: expected zone %d, got %d", i, w, elements[i].AreaID)
		}
	}
	if res.Assigned != 4 {
		t.Errorf("4 elements with a municipality expected, got %d", res.Assigned)
	}
	if len(res.Unknown) != 1 || res.Unknown[0] != "28034" {
		t.Errorf("only 28034 is a municipality the club does not know, got %v", res.Unknown)
	}
	if _, err := osmmirror.AssignZones(strings.NewReader("\x1e{not json}\n"), elements, osmmirror.ZoneRules{Municipality: resolve}); err == nil {
		t.Error("expected an error for a line that is not JSON")
	}
	if n, err := osmmirror.AssignZones(strings.NewReader(""), []osmmirror.Element{at(1, 1)}, osmmirror.ZoneRules{Municipality: resolve}); err != nil || n.Assigned != 0 || len(n.Unknown) != 0 {
		t.Errorf("no boundaries, no zones: %v %v", n, err)
	}
}

// An element in no municipality takes the one whose boundary is within a few metres of it (a sea cave that a
// boundary that follows the coast leaves out), and not one that is farther, nor one inside a hole.
func TestAssignZonesNearest(t *testing.T) {
	level8 := func(ref string) string {
		return `{"boundary":"administrative","admin_level":"8","ref:ine":"` + ref + `"}`
	}
	// Longitude 0 to 1 and latitude 40 to 41; a metre of latitude is 1/111320 of a degree.
	in := boundary(level8("28030000000"), `{"type":"Polygon","coordinates":`+square(0, 40, 1, 41)+`}`) +
		boundary(level8("28031000000"), `{"type":"Polygon","coordinates":`+square(2, 40, 3, 41)+`}`)
	at := func(lat, lon float64) osmmirror.Element {
		return osmmirror.Element{Type: "node", ID: 1, Lat: lat, Lon: lon}
	}
	elements := []osmmirror.Element{
		at(40.5, 1+5.0/111320/0.766),  // about 5 m east of the first (cos 40.5° is 0.76), outside
		at(40.5, 1+100.0/111320/0.76), // about 100 m east of it
		at(40.5, 1.5),                 // in the middle of nowhere
		at(40.5, 0.5),                 // inside the first
		at(39.99995, 2.5),             // about 5 m south of the second
	}
	zones := map[string]int64{"28030": 30, "28031": 31}
	rules := osmmirror.ZoneRules{Municipality: osmmirror.MunicipalityResolver(zones), NearestM: osmmirror.NearestZoneMetres}
	res, err := osmmirror.AssignZones(strings.NewReader(in), elements, rules)
	if err != nil {
		t.Fatal(err)
	}
	want := []int64{30, 0, 0, 30, 31}
	for i, w := range want {
		if elements[i].AreaID != w {
			t.Errorf("element %d: expected zone %d, got %d", i, w, elements[i].AreaID)
		}
	}
	if res.Assigned != 3 || res.Nearest != 2 {
		t.Errorf("3 with a zone, 2 of them by being close: %+v", res)
	}
	// Without the reach nothing is given for being close.
	elements = []osmmirror.Element{at(40.5, 1+5.0/111320/0.766)}
	rules.NearestM = 0
	if res, err := osmmirror.AssignZones(strings.NewReader(in), elements, rules); err != nil || res.Assigned != 0 || elements[0].AreaID != 0 {
		t.Errorf("no reach, no zone: %+v %v", res, err)
	}
}

// The boundary of a country or a territory gives a zone only to what no municipality holds, and a country the
// club has no zone for is not a municipality it lacks.
func TestAssignZonesCountries(t *testing.T) {
	municipality := `{"boundary":"administrative","admin_level":"8","ref:ine":"28030000000"}`
	gibraltar := `{"boundary":"administrative","admin_level":"2","ISO3166-1:alpha2":"GI"}`
	elsewhere := `{"boundary":"administrative","admin_level":"2","ISO3166-1:alpha2":"ZZ"}`
	// The territory is drawn first and holds the municipality too: the municipality still gives its zone.
	in := boundary(gibraltar, `{"type":"Polygon","coordinates":`+square(0, 0, 10, 10)+`}`) +
		boundary(elsewhere, `{"type":"Polygon","coordinates":`+square(20, 20, 30, 30)+`}`) +
		boundary(municipality, `{"type":"Polygon","coordinates":`+square(0, 0, 2, 2)+`}`)
	at := func(lat, lon float64) osmmirror.Element {
		return osmmirror.Element{Type: "node", ID: 1, Lat: lat, Lon: lon}
	}
	elements := []osmmirror.Element{at(1, 1), at(5, 5), at(25, 25), at(50, 50)}
	rules := osmmirror.ZoneRules{
		Municipality: osmmirror.MunicipalityResolver(map[string]int64{"28030": 30}),
		Country:      osmmirror.CountryResolver(map[string]int64{"GI": 500}),
	}
	res, err := osmmirror.AssignZones(strings.NewReader(in), elements, rules)
	if err != nil {
		t.Fatal(err)
	}
	for i, w := range []int64{30, 500, 0, 0} {
		if elements[i].AreaID != w {
			t.Errorf("element %d: expected zone %d, got %d", i, w, elements[i].AreaID)
		}
	}
	if res.Assigned != 2 || len(res.Unknown) != 0 {
		t.Errorf("2 with a zone and nothing unknown (a country is no municipality): %+v", res)
	}
}

// Ceuta is a boundary written down, because its relation is incomplete in the extract of Spain: an element
// in it takes the zone of its code, and a code the club has no zone for is reported.
func TestAssignZonesFixed(t *testing.T) {
	at := func(lat, lon float64) osmmirror.Element {
		return osmmirror.Element{Type: "node", ID: 1, Lat: lat, Lon: lon}
	}
	elements := []osmmirror.Element{
		at(35.9117, -5.3794), // the Cueva de Benzú, in the west of Ceuta
		at(35.8904, -5.2906), // the Minas de Cobre, on Monte Hacho
		at(35.8910, -5.4000), // the sea, west of Ceuta
		at(36.1261, -5.3455), // Gibraltar
	}
	rules := osmmirror.ZoneRules{
		Municipality: osmmirror.MunicipalityResolver(map[string]int64{}),
		Fixed:        osmmirror.FixedBoundaries(),
		Zones:        map[string]int64{"51001": 18},
	}
	res, err := osmmirror.AssignZones(strings.NewReader(""), elements, rules)
	if err != nil {
		t.Fatal(err)
	}
	for i, w := range []int64{18, 18, 0, 0} {
		if elements[i].AreaID != w {
			t.Errorf("element %d: expected zone %d, got %d", i, w, elements[i].AreaID)
		}
	}
	if res.Assigned != 2 || len(res.Unknown) != 0 {
		t.Errorf("two in Ceuta: %+v", res)
	}
	rules.Zones = nil
	res, err = osmmirror.AssignZones(strings.NewReader(""), []osmmirror.Element{at(35.9117, -5.3794)}, rules)
	if err != nil || res.Assigned != 0 || len(res.Unknown) != 1 || res.Unknown[0] != "51001" {
		t.Errorf("a code with no zone in the club is reported: %+v %v", res, err)
	}
}
