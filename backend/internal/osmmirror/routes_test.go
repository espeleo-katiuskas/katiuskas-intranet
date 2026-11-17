package osmmirror_test

import (
	"math"
	"strings"
	"testing"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/osmmirror"
)

// A canyon is a route relation: its outline is a MultiLineString of its member ways, its point is the
// middle of them laid end to end (a way that is drawn the other way round is turned), and the escapes of
// OPL are undone in the tags.
func TestReadRoutes(t *testing.T) {
	opl := strings.Join([]string{
		"n1 v1 dV c0 t2020-01-01T00:00:00Z i0 u T x0 y0",
		"n2 v1 dV c0 t2020-01-01T00:00:00Z i0 u T x0 y1",
		"n3 v1 dV c0 t2020-01-01T00:00:00Z i0 u T x0 y2",
		"n4 v1 dV c0 t2020-01-01T00:00:00Z i0 u T x0 y4",
		"n9 v1 dV c0 t2020-01-01T00:00:00Z i0 u Thighway=path x5 y5",
		// Two ways of the stream, the second one drawn in the opposite direction.
		"w10 v1 dV c0 t2020-01-01T00:00:00Z i0 u Twaterway=stream Nn1,n2",
		"w11 v1 dV c0 t2020-01-01T00:00:00Z i0 u Twaterway=stream Nn4,n3,n2",
		"w12 v1 dV c0 t2020-01-01T00:00:00Z i0 u Thighway=path Nn9,n9",
		"r100 v7 dV c0 t2023-04-10T14:40:18Z i0 u Tmax:abseil=20,name=Barranco%20%de%20%la%20%Palomera%2c%,route=canyoning,type=route Mw10@,w11@,n9@",
		// A route that is not a canyon, a canyon with no way in the file, and one that was deleted.
		"r101 v1 dV c0 t2023-04-10T14:40:18Z i0 u Tname=GR,route=hiking,type=route Mw12@",
		"r102 v1 dV c0 t2023-04-10T14:40:18Z i0 u Tname=Sin%20%vías,route=canyoning,type=route Mw99@",
		"r103 v1 dD c0 t2023-04-10T14:40:18Z i0 u Troute=canyoning,type=route Mw10@",
	}, "\n") + "\n"
	got, err := osmmirror.ReadRoutes(strings.NewReader(opl))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("one canyon expected, got %d: %+v", len(got), got)
	}
	c := got[0]
	if c.Type != "relation" || c.ID != 100 || c.Category != "canyon" || c.Version != 7 ||
		c.Timestamp.Unix() != 1681137618 {
		t.Errorf("unexpected canyon: %+v", c)
	}
	if c.Tags["name"] != "Barranco de la Palomera," || c.Tags["max:abseil"] != "20" {
		t.Errorf("the escapes are undone: %+v", c.Tags)
	}
	// The chain is (0,0)-(0,1)-(0,2)-(0,4) once the second way is turned: it is 4 long, so its middle is at 2.
	if math.Abs(c.Lat-2) > 1e-9 || math.Abs(c.Lon) > 1e-9 {
		t.Errorf("the point is the middle of the stream, got %v, %v", c.Lat, c.Lon)
	}
	if c.Geometry["type"] != "MultiLineString" {
		t.Fatalf("expected a MultiLineString: %+v", c.Geometry)
	}
	if parts, _ := c.Geometry["coordinates"].([]any); len(parts) != 2 {
		t.Errorf("one line per member way expected: %+v", c.Geometry)
	}
}

func TestReadRoutesErrors(t *testing.T) {
	if _, err := osmmirror.ReadRoutes(strings.NewReader("x\n")); err == nil {
		t.Error("expected an error for a line that is not OPL")
	}
	if got, err := osmmirror.ReadRoutes(strings.NewReader("")); err != nil || len(got) != 0 {
		t.Errorf("an empty file has no routes: %v %+v", err, got)
	}
}
