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
		boundary(level8("28034000000"), `{"type":"Polygon","coordinates":`+square(19, 19, 20, 20)+`}`) // Not in the catalogue.
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
	}
	// The zones of the club, by INE code; 28034 is on the map but not in the catalogue.
	resolve := osmmirror.MunicipalityResolver(map[string]int64{"28030": 30, "28031": 31, "28032": 32, "28033": 33})
	n, err := osmmirror.AssignZones(strings.NewReader(in), elements, resolve)
	if err != nil {
		t.Fatal(err)
	}
	want := []int64{30, 31, 0, 32, 33, 0, 0, 0, 0}
	for i, w := range want {
		if elements[i].AreaID != w {
			t.Errorf("element %d: expected zone %d, got %d", i, w, elements[i].AreaID)
		}
	}
	if n != 4 {
		t.Errorf("4 elements with a municipality expected, got %d", n)
	}
	if _, err := osmmirror.AssignZones(strings.NewReader("\x1e{not json}\n"), elements, resolve); err == nil {
		t.Error("expected an error for a line that is not JSON")
	}
	if n, err := osmmirror.AssignZones(strings.NewReader(""), []osmmirror.Element{at(1, 1)}, resolve); err != nil || n != 0 {
		t.Errorf("no boundaries, no zones: %d %v", n, err)
	}
}
