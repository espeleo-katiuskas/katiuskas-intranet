package osmmirror

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/areas"
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

// ZoneResolver says what a boundary is, from its tags: `code`, its code in the system of its country ("" if it is
// not a boundary that this rule takes for a zone), and `zone`, the id of the zone of the club that has that code
// (0 if the club has none: the boundary is known but the catalogue is behind, which AssignZones reports). It is
// where the rule of each country lives; MunicipalityResolver is Spain's.
type ZoneResolver func(tags map[string]any) (zone int64, code string)

// MunicipalityResolver is the rule of Spain: a boundary tagged `boundary=administrative` and `admin_level=8`
// with a `ref:ine` is the municipality whose code (the first five digits of the reference, whose eleven are the
// municipality followed by the entity) is in ids, the zones of kind Municipio by their code and the autonomous
// cities by the code of their municipality. Another country would add a rule of its own (its own reference, its
// own level) once its zones are in the catalogue.
func MunicipalityResolver(ids map[string]int64) ZoneResolver {
	return func(tags map[string]any) (int64, string) {
		if tags["boundary"] != "administrative" || tags["admin_level"] != "8" {
			return 0, ""
		}
		// Only a Spanish reference counts: an extract that crosses the border has Portugal's parishes, whose
		// `ref:ine` is six digits of another system (areas.MunicipalityCode).
		ref, _ := tags["ref:ine"].(string)
		code := areas.MunicipalityCode(ref)
		if code == "" {
			return 0, ""
		}
		return ids[code], code
	}
}

// CountryResolver is the rule for the boundaries of a country or of a territory that is not a state (Andorra,
// Gibraltar): a boundary tagged `boundary=administrative` and `admin_level=2` with an ISO 3166-1 code, whose zone
// is the one of the catalogue that has that code (ids: the countries and the territories by their ISO code). It
// is only for what no municipality holds, and a country the club has no zone for is not reported.
func CountryResolver(ids map[string]int64) ZoneResolver {
	return func(tags map[string]any) (int64, string) {
		if tags["boundary"] != "administrative" || tags["admin_level"] != "2" {
			return 0, ""
		}
		code, _ := tags["ISO3166-1:alpha2"].(string)
		if code == "" {
			code, _ = tags["ISO3166-1"].(string)
		}
		return ids[code], code
	}
}

// NearestZoneMetres is how far from the boundary of a municipality an element that is in none may be and still
// take it: the sea caves, which a boundary that follows the coast leaves a few metres out. It is short on
// purpose: a longer one would give a municipality to what is across a land border.
const NearestZoneMetres = 15

// ZoneRules say what a boundary is, for AssignZones.
type ZoneRules struct {
	// Municipality is the rule of the boundaries that are the zone of what they hold (a municipality, in Spain),
	// and the only one whose boundaries the club may lack a zone for (it is reported).
	Municipality ZoneResolver
	// Country is the rule of the boundaries of countries and territories, which only give a zone to what no
	// municipality holds. May be nil.
	Country ZoneResolver
	// Fixed are boundaries that the extracts do not have as a polygon, and count as municipal ones: their Code
	// is looked for in the same map as the municipalities'. The map of codes to zones is Zones.
	Fixed []FixedBoundary
	// Zones are the zones of the club by the code of a municipality, for the Fixed boundaries.
	Zones map[string]int64
	// NearestM is how close, in metres, to a municipal boundary an element in none takes that municipality
	// (0: it does not).
	NearestM float64
}

// ZoneResult is what AssignZones did: how many elements got a zone and, of those, how many by being close to a
// boundary and not inside it, and the codes of the boundaries that the municipal rule knew but the club has no
// zone for, sorted and without repeats.
type ZoneResult struct {
	Assigned int
	Nearest  int
	Unknown  []string
}

// nearest keeps, for each element, the closest municipality that is not holding it, if it is within reach.
type nearest struct {
	maxM float64
	dist []float64
	zone []int64
}

func newNearest(n int, maxM float64) *nearest {
	d := make([]float64, n)
	for i := range d {
		d[i] = math.Inf(1)
	}
	return &nearest{maxM: maxM, dist: d, zone: make([]int64, n)}
}

// AssignZones reads the polygons that `osmium export -f geojsonseq` wrote (one feature per line) and sets the
// AreaID of the elements whose point is inside the polygon of a municipal boundary (the first that holds it),
// then of those still in none that are inside the boundary of a country or a territory, then, if the rules say
// so, of those still in none that are very close to a municipal boundary. It gives how many it set, and the
// codes of the boundaries that have no zone in the club. Elements in no zone are left with none.
func AssignZones(r io.Reader, elements []Element, rules ZoneRules) (ZoneResult, error) {
	grid := map[cell][]int{}
	for i := range elements {
		c := cellOf(elements[i].Lon, elements[i].Lat)
		grid[c] = append(grid[c], i)
	}
	var near *nearest
	if rules.NearestM > 0 {
		near = newNearest(len(elements), rules.NearestM)
	}
	type feature struct {
		Geometry struct {
			Type        string          `json:"type"`
			Coordinates json.RawMessage `json:"coordinates"`
		} `json:"geometry"`
		Properties map[string]any `json:"properties"`
	}
	result := ZoneResult{}
	unknown := map[string]bool{}
	for _, fixed := range rules.Fixed {
		if zone := rules.Zones[fixed.Code]; zone != 0 {
			result.Assigned += assignIn(fixed.Polygons, zone, elements, grid, near)
		} else {
			unknown[fixed.Code] = true
		}
	}
	type late struct {
		zone     int64
		polygons []polygon
	}
	var countries []late
	br := bufio.NewReaderSize(r, 1<<20)
	for line := 1; ; line++ {
		raw, err := br.ReadBytes('\n')
		raw = bytes.Trim(raw, "\x1e \r\n")
		if len(raw) > 0 {
			var f feature
			if jsonErr := json.Unmarshal(raw, &f); jsonErr != nil {
				return result, fmt.Errorf("boundary line %d: %w", line, jsonErr)
			}
			zone, code := rules.Municipality(f.Properties)
			var countryZone int64
			if zone == 0 && code == "" && rules.Country != nil {
				countryZone, _ = rules.Country(f.Properties)
			}
			switch {
			case zone != 0 || countryZone != 0:
				polygons, convErr := polygonsOf(f.Geometry.Type, f.Geometry.Coordinates)
				if convErr != nil {
					return result, fmt.Errorf("boundary line %d: %w", line, convErr)
				}
				if zone != 0 {
					result.Assigned += assignIn(polygons, zone, elements, grid, near)
				} else {
					countries = append(countries, late{countryZone, polygons})
				}
			case code != "":
				unknown[code] = true // A boundary of a municipality that the club has no zone for.
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return result, err
		}
	}
	for _, c := range countries {
		result.Assigned += assignIn(c.polygons, c.zone, elements, grid, nil)
	}
	if near != nil {
		for i := range elements {
			if elements[i].AreaID == 0 && near.zone[i] != 0 && near.dist[i] <= near.maxM {
				elements[i].AreaID = near.zone[i]
				result.Assigned++
				result.Nearest++
			}
		}
	}
	for code := range unknown {
		result.Unknown = append(result.Unknown, code)
	}
	sort.Strings(result.Unknown)
	return result, nil
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
// With near, it also notes the elements that are outside but within its reach of the boundary of a polygon.
func assignIn(polygons []polygon, zone int64, elements []Element, grid map[cell][]int, near *nearest) int {
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
		// The box that holds the polygon, and, to look for what is close to it, that box widened by the reach.
		padLat := 0.0
		if near != nil {
			padLat = near.maxM / metresPerDegree
		}
		padLon := padLat / math.Max(math.Cos((minY+maxY)/2*math.Pi/180), 0.01)
		lo, hi := cellOf(minX-padLon, minY-padLat), cellOf(maxX+padLon, maxY+padLat)
		for x := lo.x; x <= hi.x; x++ {
			for y := lo.y; y <= hi.y; y++ {
				for _, i := range grid[cell{x, y}] {
					e := &elements[i]
					if e.AreaID != 0 {
						continue
					}
					inBox := e.Lon >= minX && e.Lon <= maxX && e.Lat >= minY && e.Lat <= maxY
					if inBox && p.contains(e.Lon, e.Lat) {
						e.AreaID = zone
						assigned++
						continue
					}
					if near == nil || e.Lon < minX-padLon || e.Lon > maxX+padLon || e.Lat < minY-padLat || e.Lat > maxY+padLat {
						continue
					}
					if d := p.distanceM(e.Lon, e.Lat); d < near.dist[i] {
						near.dist[i], near.zone[i] = d, zone
					}
				}
			}
		}
	}
	return assigned
}
