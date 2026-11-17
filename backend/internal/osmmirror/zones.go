package osmmirror

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
)

// The zone of each element: the one whose boundary holds its point (a municipality, in Spain). The
// boundaries come from the same extract as the elements (`osmium export` of the relations with admin_level=8,
// as polygons), so the zones are as new as the data, and are read one feature at a time: Spain has about 8,000
// of them and 80 MB of coordinates, which would be a lot to keep in memory to test a few thousand points.

// zoneCell is the side, in degrees, of the cells of the grid that finds the points inside the box of a boundary.
const zoneCell = 0.1

type cell struct{ x, y int }

func cellOf(lon, lat float64) cell {
	return cell{int(math.Floor(lon / zoneCell)), int(math.Floor(lat / zoneCell))}
}

// ZoneResolver says which zone a boundary is, from its tags: the id of a zone of the club, or 0 if it is not
// the boundary of any. It is where the rule of each country lives; MunicipalityResolver is Spain's.
type ZoneResolver func(tags map[string]any) int64

// MunicipalityResolver is the rule of Spain: a boundary tagged `boundary=administrative` and `admin_level=8`
// with a `ref:ine` is the municipality whose code (the first five digits of the reference, whose eleven are the
// municipality followed by the entity) is in ids, the zones of kind Municipio by their code. Another country
// would add a rule of its own (its own reference, its own level) once its zones are in the catalogue.
func MunicipalityResolver(ids map[string]int64) ZoneResolver {
	return func(tags map[string]any) int64 {
		if tags["boundary"] != "administrative" || tags["admin_level"] != "8" {
			return 0
		}
		ref, _ := tags["ref:ine"].(string)
		if len(ref) < 5 {
			return 0
		}
		for _, r := range ref[:5] {
			if r < '0' || r > '9' {
				return 0
			}
		}
		return ids[ref[:5]]
	}
}

// AssignZones reads the polygons that `osmium export -f geojsonseq` wrote (one feature per line) and sets the
// AreaID of the elements whose point is inside the polygon of a boundary that `resolve` knows: the first that
// holds it. It gives how many it set. Elements in no zone are left with none.
func AssignZones(r io.Reader, elements []Element, resolve ZoneResolver) (int, error) {
	grid := map[cell][]int{}
	for i := range elements {
		c := cellOf(elements[i].Lon, elements[i].Lat)
		grid[c] = append(grid[c], i)
	}
	type feature struct {
		Geometry struct {
			Type        string          `json:"type"`
			Coordinates json.RawMessage `json:"coordinates"`
		} `json:"geometry"`
		Properties map[string]any `json:"properties"`
	}
	assigned := 0
	br := bufio.NewReaderSize(r, 1<<20)
	for line := 1; ; line++ {
		raw, err := br.ReadBytes('\n')
		raw = bytes.Trim(raw, "\x1e \r\n")
		if len(raw) > 0 {
			var f feature
			if jsonErr := json.Unmarshal(raw, &f); jsonErr != nil {
				return assigned, fmt.Errorf("boundary line %d: %w", line, jsonErr)
			}
			if zone := resolve(f.Properties); zone != 0 {
				polygons, convErr := polygonsOf(f.Geometry.Type, f.Geometry.Coordinates)
				if convErr != nil {
					return assigned, fmt.Errorf("boundary line %d: %w", line, convErr)
				}
				assigned += assignIn(polygons, zone, elements, grid)
			}
		}
		if err == io.EOF {
			return assigned, nil
		}
		if err != nil {
			return assigned, err
		}
	}
}

// polygonsOf reads the coordinates of a Polygon or a MultiPolygon.
func polygonsOf(kind string, coordinates json.RawMessage) ([]polygon, error) {
	switch kind {
	case "Polygon":
		var rings []ring
		if err := json.Unmarshal(coordinates, &rings); err != nil {
			return nil, err
		}
		return []polygon{polygon(rings)}, nil
	case "MultiPolygon":
		var many [][]ring
		if err := json.Unmarshal(coordinates, &many); err != nil {
			return nil, err
		}
		out := make([]polygon, len(many))
		for i, rings := range many {
			out[i] = polygon(rings)
		}
		return out, nil
	}
	return nil, nil // Not a shape that holds a point.
}

// assignIn sets `zone` on the elements, still with none, that are inside any of the polygons, and says how many.
func assignIn(polygons []polygon, zone int64, elements []Element, grid map[cell][]int) int {
	assigned := 0
	for _, p := range polygons {
		if len(p) == 0 || len(p[0]) < 3 {
			continue
		}
		minX, minY, maxX, maxY := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
		for _, c := range p[0] {
			minX, maxX = math.Min(minX, c[0]), math.Max(maxX, c[0])
			minY, maxY = math.Min(minY, c[1]), math.Max(maxY, c[1])
		}
		lo, hi := cellOf(minX, minY), cellOf(maxX, maxY)
		for x := lo.x; x <= hi.x; x++ {
			for y := lo.y; y <= hi.y; y++ {
				for _, i := range grid[cell{x, y}] {
					e := &elements[i]
					if e.AreaID != 0 || e.Lon < minX || e.Lon > maxX || e.Lat < minY || e.Lat > maxY {
						continue
					}
					if p.contains(e.Lon, e.Lat) {
						e.AreaID = zone
						assigned++
					}
				}
			}
		}
	}
	return assigned
}
