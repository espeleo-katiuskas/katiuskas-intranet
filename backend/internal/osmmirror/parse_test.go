package osmmirror_test

import (
	"math"
	"strings"
	"testing"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/osmmirror"
)

func seq(lines ...string) string {
	var b strings.Builder
	for _, l := range lines {
		b.WriteString("\x1e" + l + "\n")
	}
	return b.String()
}

func feature(geometry, props string) string {
	return `{"type":"Feature","geometry":` + geometry + `,"properties":` + props + `}`
}

const point = `{"type":"Point","coordinates":[-3.6,40.8]}`

// The filters keep caves, canyons, huts and via ferratas, once each, and drop what comes out of an extract only
// because a way refers to it.
func TestReadFilters(t *testing.T) {
	square := `{"type":"MultiPolygon","coordinates":[[[[-2.0,40.0],[-2.0,40.2],[-1.8,40.2],[-1.8,40.0],[-2.0,40.0]]]]}`
	ring := `{"type":"LineString","coordinates":[[-2.0,40.0],[-2.0,40.2],[-1.8,40.2],[-1.8,40.0],[-2.0,40.0]]}`
	line := `{"type":"LineString","coordinates":[[0,0],[0,1],[0,3]]}`
	in := seq(
		feature(point, `{"@type":"node","@id":1,"@version":3,"@timestamp":1700000000,"natural":"cave_entrance","name":"Cueva"}`),
		// A closed way comes as a line and as a polygon: the polygon is kept, once.
		feature(ring, `{"@type":"way","@id":2,"@version":1,"@timestamp":1700000000,"tourism":"wilderness_hut","name":"Refugio"}`),
		feature(square, `{"@type":"way","@id":2,"@version":1,"@timestamp":1700000000,"tourism":"wilderness_hut","name":"Refugio"}`),
		feature(line, `{"@type":"way","@id":3,"@version":2,"@timestamp":1700000000,"highway":"via_ferrata","name":"Vía"}`),
		// A hut by its tags, by a basic_hut shelter and by its name; but not a bus stop, a picnic shelter or a
		// shelter with another name.
		feature(point, `{"@type":"node","@id":4,"@version":1,"@timestamp":1,"amenity":"shelter","shelter_type":"basic_hut"}`),
		feature(point, `{"@type":"node","@id":5,"@version":1,"@timestamp":1,"amenity":"shelter","name":"Refugio Arroyetas"}`),
		feature(point, `{"@type":"node","@id":6,"@version":1,"@timestamp":1,"amenity":"shelter","shelter_type":"public_transport"}`),
		feature(point, `{"@type":"node","@id":7,"@version":1,"@timestamp":1,"amenity":"shelter","name":"Parada"}`),
		feature(point, `{"@type":"node","@id":8,"@version":1,"@timestamp":1,"amenity":"shelter","shelter_type":"picnic_shelter","name":"Refugio"}`),
		// A canyon mapped as a stream tagged sport=canyoning.
		feature(line, `{"@type":"way","@id":10,"@version":1,"@timestamp":1,"sport":"canyoning","waterway":"stream","name":"Barranco"}`),
		// What is there only because it is referenced.
		feature(point, `{"@type":"node","@id":9,"@version":1,"@timestamp":1,"natural":"tree"}`),
	)
	got, err := osmmirror.Read(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	byID := map[int64]osmmirror.Element{}
	for _, e := range got {
		byID[e.ID] = e
	}
	if len(got) != 6 {
		t.Fatalf("expected 6 elements, got %d: %+v", len(got), got)
	}
	if byID[10].Category != "canyon" {
		t.Errorf("a stream tagged sport=canyoning is a canyon: %+v", byID[10])
	}
	cave := byID[1]
	if cave.Category != "cave" || cave.Type != "node" || cave.Version != 3 || cave.Lat != 40.8 || cave.Lon != -3.6 ||
		cave.Geometry != nil || cave.Timestamp.Unix() != 1700000000 || cave.Tags["name"] != "Cueva" {
		t.Errorf("unexpected cave: %+v", cave)
	}
	hut := byID[2]
	if hut.Category != "hut" || hut.Type != "way" || hut.Geometry == nil || hut.Geometry["type"] != "MultiPolygon" {
		t.Errorf("the polygon of the way is kept: %+v", hut)
	}
	if math.Abs(hut.Lat-40.1) > 1e-6 || math.Abs(hut.Lon+1.9) > 1e-6 {
		t.Errorf("the point of a square is its centre, got %v, %v", hut.Lat, hut.Lon)
	}
	via := byID[3]
	if via.Category != "via_ferrata" || via.Geometry["type"] != "LineString" || math.Abs(via.Lat-1.5) > 1e-6 {
		t.Errorf("the point of a line is its middle: %+v", via)
	}
	for _, id := range []int64{4, 5} {
		if byID[id].Category != "hut" {
			t.Errorf("element %d should be a hut: %+v", id, byID[id])
		}
	}
}

// The point of a shape is inside it even when its centre is not: an L-shaped building.
func TestReadPointInsideL(t *testing.T) {
	// An L whose corner is at the origin: the centroid of its corners falls in the empty angle.
	l := `{"type":"Polygon","coordinates":[[[0,0],[10,0],[10,1],[1,1],[1,10],[0,10],[0,0]]]}`
	in := seq(feature(l, `{"@type":"way","@id":1,"@version":1,"@timestamp":1,"tourism":"alpine_hut"}`))
	got, err := osmmirror.Read(strings.NewReader(in))
	if err != nil || len(got) != 1 {
		t.Fatalf("%v %+v", err, got)
	}
	x, y := got[0].Lon, got[0].Lat
	inArm := (x >= 0 && x <= 10 && y >= 0 && y <= 1) || (x >= 0 && x <= 1 && y >= 0 && y <= 10)
	if !inArm {
		t.Errorf("the point (%v, %v) is not inside the L", x, y)
	}
}

func TestReadErrors(t *testing.T) {
	if _, err := osmmirror.Read(strings.NewReader("\x1e{not json}\n")); err == nil {
		t.Error("expected an error for a line that is not JSON")
	}
	in := seq(feature(point, `{"natural":"cave_entrance"}`))
	if _, err := osmmirror.Read(strings.NewReader(in)); err == nil {
		t.Error("expected an error for a feature without its type and id")
	}
	// No shape osmium could build: skipped, not an error.
	in = seq(feature(`{"type":"Polygon","coordinates":[]}`, `{"@type":"way","@id":1,"@version":1,"@timestamp":1,"tourism":"alpine_hut"}`))
	if got, err := osmmirror.Read(strings.NewReader(in)); err != nil || len(got) != 0 {
		t.Errorf("a shape with no point is skipped: %v %+v", err, got)
	}
}
