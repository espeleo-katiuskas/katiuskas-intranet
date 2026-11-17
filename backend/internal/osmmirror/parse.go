package osmmirror

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"time"
)

// Read takes what `osmium export -f geojsonseq -a type,id,version,timestamp` wrote (one GeoJSON feature per
// line, each after a record separator) and gives the elements that match a filter, once each: a closed way
// comes out twice, as a line and as a polygon, and the polygon is kept. The point of a way or a relation is
// one inside its shape (see geometry.go).
func Read(r io.Reader) ([]Element, error) {
	type feature struct {
		Geometry   map[string]any `json:"geometry"`
		Properties map[string]any `json:"properties"`
	}
	br := bufio.NewReaderSize(r, 1<<20)
	byKey := map[Key]Element{}
	var order []Key
	for line := 1; ; line++ {
		raw, err := br.ReadBytes('\n')
		raw = bytes.Trim(raw, "\x1e \r\n")
		if len(raw) > 0 {
			var f feature
			if jsonErr := json.Unmarshal(raw, &f); jsonErr != nil {
				return nil, fmt.Errorf("line %d: %w", line, jsonErr)
			}
			e, ok, convErr := toElement(f.Geometry, f.Properties)
			if convErr != nil {
				return nil, fmt.Errorf("line %d: %w", line, convErr)
			}
			if ok {
				k := e.Key()
				prev, seen := byKey[k]
				switch {
				case !seen:
					byKey[k] = e
					order = append(order, k)
				case prev.Geometry != nil && isLine(prev.Geometry) && !isLine(e.Geometry):
					// A polygon of the same way wins over its line.
					byKey[k] = e
				}
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	out := make([]Element, 0, len(order))
	for _, k := range order {
		out = append(out, byKey[k])
	}
	return out, nil
}

func isLine(g map[string]any) bool {
	t, _ := g["type"].(string)
	return t == "LineString" || t == "MultiLineString"
}

// metaKeys are the properties that osmium adds, not tags.
var metaKeys = map[string]bool{"@type": true, "@id": true, "@version": true, "@timestamp": true}

func toElement(geometry, properties map[string]any) (Element, bool, error) {
	tags := map[string]string{}
	for k, v := range properties {
		if metaKeys[k] {
			continue
		}
		if s, ok := v.(string); ok {
			tags[k] = s
		}
	}
	category := Classify(tags)
	if category == "" {
		return Element{}, false, nil
	}
	typ, _ := properties["@type"].(string)
	id, okID := properties["@id"].(float64)
	version, _ := properties["@version"].(float64)
	stamp, _ := properties["@timestamp"].(float64)
	if (typ != "node" && typ != "way" && typ != "relation") || !okID || id != math.Trunc(id) {
		return Element{}, false, fmt.Errorf("a feature without its OpenStreetMap type and id: %v", properties)
	}
	lat, lon, ok := pointOf(geometry)
	if !ok || lat < -90 || lat > 90 || lon < -180 || lon > 180 {
		// A shape that osmium could not build, or that has no point: nothing to put on a map.
		return Element{}, false, nil
	}
	e := Element{
		Type: typ, ID: int64(id), Version: int(version), Timestamp: time.Unix(int64(stamp), 0).UTC(),
		Category: category, Lat: round7(lat), Lon: round7(lon), Tags: tags,
	}
	if kind, _ := geometry["type"].(string); kind != "Point" {
		e.Geometry = geometry
	}
	return e, true, nil
}

// round7 keeps seven decimals, which is what OpenStreetMap stores (about a centimetre).
func round7(v float64) float64 { return math.Round(v*1e7) / 1e7 }
