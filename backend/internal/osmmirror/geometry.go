package osmmirror

import (
	"math"
	"sort"
)

// The point of a shape, which is what lists and maps use for an element that is not a node. All the
// coordinates are [longitude, latitude], as in GeoJSON, and the maths is planar: the shapes are a few
// metres to a few kilometres across, where that is as good as anything.

type ring [][2]float64

// pointInRing says whether (x, y) is inside a ring (even-odd rule).
func pointInRing(r ring, x, y float64) bool {
	inside := false
	for i, j := 0, len(r)-1; i < len(r); j, i = i, i+1 {
		if (r[i][1] > y) != (r[j][1] > y) &&
			x < (r[j][0]-r[i][0])*(y-r[i][1])/(r[j][1]-r[i][1])+r[i][0] {
			inside = !inside
		}
	}
	return inside
}

// polygon is an outer ring and its holes.
type polygon []ring

func (p polygon) contains(x, y float64) bool {
	if len(p) == 0 || !pointInRing(p[0], x, y) {
		return false
	}
	for _, hole := range p[1:] {
		if pointInRing(hole, x, y) {
			return false
		}
	}
	return true
}

// ringArea is the signed area of a ring and its centroid (shoelace formula).
func ringArea(r ring) (area, cx, cy float64) {
	for i, j := 0, len(r)-1; i < len(r); j, i = i, i+1 {
		cross := r[j][0]*r[i][1] - r[i][0]*r[j][1]
		area += cross
		cx += (r[j][0] + r[i][0]) * cross
		cy += (r[j][1] + r[i][1]) * cross
	}
	area /= 2
	if area == 0 {
		return 0, 0, 0
	}
	return area, cx / (6 * area), cy / (6 * area)
}

// interiorPoint is a point that is inside the polygon: its centroid when that is inside (the usual case),
// else the middle of the widest stretch of a horizontal line through the middle of its height. The centre
// of an L-shaped building or a ring can be outside it.
func interiorPoint(p polygon) (x, y float64, ok bool) {
	if len(p) == 0 || len(p[0]) < 3 {
		return 0, 0, false
	}
	if area, cx, cy := ringArea(p[0]); area != 0 && p.contains(cx, cy) {
		return cx, cy, true
	}
	minY, maxY := math.Inf(1), math.Inf(-1)
	for _, c := range p[0] {
		minY, maxY = math.Min(minY, c[1]), math.Max(maxY, c[1])
	}
	// A few heights, in case the middle one runs along an edge.
	for _, f := range []float64{0.5, 0.25, 0.75, 0.4, 0.6} {
		yy := minY + (maxY-minY)*f
		var xs []float64
		for _, r := range p {
			for i, j := 0, len(r)-1; i < len(r); j, i = i, i+1 {
				if (r[i][1] > yy) != (r[j][1] > yy) {
					xs = append(xs, (r[j][0]-r[i][0])*(yy-r[i][1])/(r[j][1]-r[i][1])+r[i][0])
				}
			}
		}
		sort.Float64s(xs)
		best, bx := -1.0, 0.0
		for i := 0; i+1 < len(xs); i += 2 {
			if w := xs[i+1] - xs[i]; w > best {
				best, bx = w, (xs[i]+xs[i+1])/2
			}
		}
		if best > 0 && p.contains(bx, yy) {
			return bx, yy, true
		}
	}
	return 0, 0, false
}

// lineMiddle is the point halfway along a line.
func lineMiddle(line [][2]float64) (x, y float64, ok bool) {
	if len(line) == 0 {
		return 0, 0, false
	}
	if len(line) == 1 {
		return line[0][0], line[0][1], true
	}
	var total float64
	for i := 1; i < len(line); i++ {
		total += math.Hypot(line[i][0]-line[i-1][0], line[i][1]-line[i-1][1])
	}
	if total == 0 {
		return line[0][0], line[0][1], true
	}
	half := total / 2
	for i := 1; i < len(line); i++ {
		d := math.Hypot(line[i][0]-line[i-1][0], line[i][1]-line[i-1][1])
		if half <= d && d > 0 {
			t := half / d
			return line[i-1][0] + t*(line[i][0]-line[i-1][0]), line[i-1][1] + t*(line[i][1]-line[i-1][1]), true
		}
		half -= d
	}
	last := line[len(line)-1]
	return last[0], last[1], true
}

// pointOf is the point that stands for a GeoJSON geometry: a Point is itself, a polygon (or the biggest of a
// multipolygon) gives a point inside it, a line gives its middle.
func pointOf(geometry map[string]any) (lat, lon float64, ok bool) {
	kind, _ := geometry["type"].(string)
	switch kind {
	case "Point":
		c, ok := coordinate(geometry["coordinates"])
		return c[1], c[0], ok
	case "LineString":
		line, ok := coordinates(geometry["coordinates"])
		if !ok {
			return 0, 0, false
		}
		x, y, ok := lineMiddle(line)
		return y, x, ok
	case "Polygon":
		p, ok := polygonOf(geometry["coordinates"])
		if !ok {
			return 0, 0, false
		}
		x, y, ok := interiorPoint(p)
		return y, x, ok
	case "MultiPolygon":
		list, _ := geometry["coordinates"].([]any)
		var best polygon
		bestArea := -1.0
		for _, item := range list {
			p, ok := polygonOf(item)
			if !ok || len(p) == 0 {
				continue
			}
			if a, _, _ := ringArea(p[0]); math.Abs(a) > bestArea {
				best, bestArea = p, math.Abs(a)
			}
		}
		x, y, ok := interiorPoint(best)
		return y, x, ok
	}
	return 0, 0, false
}

func coordinate(v any) ([2]float64, bool) {
	a, _ := v.([]any)
	if len(a) < 2 {
		return [2]float64{}, false
	}
	x, okx := a[0].(float64)
	y, oky := a[1].(float64)
	return [2]float64{x, y}, okx && oky
}

func coordinates(v any) ([][2]float64, bool) {
	list, _ := v.([]any)
	out := make([][2]float64, 0, len(list))
	for _, item := range list {
		c, ok := coordinate(item)
		if !ok {
			return nil, false
		}
		out = append(out, c)
	}
	return out, len(out) > 0
}

func polygonOf(v any) (polygon, bool) {
	rings, _ := v.([]any)
	var p polygon
	for _, item := range rings {
		cs, ok := coordinates(item)
		if !ok {
			return nil, false
		}
		p = append(p, ring(cs))
	}
	return p, len(p) > 0
}

// metresPerDegree is the length of a degree of latitude, and of longitude at the equator.
const metresPerDegree = 111320

// distanceM is how far, in metres, the point (x, y) is from the boundary of the polygon (its outer ring and its
// holes), on a plane around the point: good for the few metres that it is used for.
func (p polygon) distanceM(x, y float64) float64 {
	k := math.Cos(y * math.Pi / 180)
	best := math.Inf(1)
	for _, r := range p {
		for i, j := 0, len(r)-1; i < len(r); j, i = i, i+1 {
			ax, ay := (r[j][0]-x)*k, r[j][1]-y
			bx, by := (r[i][0]-x)*k, r[i][1]-y
			dx, dy := bx-ax, by-ay
			t := 0.0
			if l := dx*dx + dy*dy; l > 0 {
				t = math.Max(0, math.Min(1, -(ax*dx+ay*dy)/l))
			}
			best = math.Min(best, math.Hypot(ax+t*dx, ay+t*dy))
		}
	}
	return best * metresPerDegree
}
