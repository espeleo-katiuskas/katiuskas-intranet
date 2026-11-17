package places

import (
	"encoding/json"
	"testing"
)

func snapshot(t *testing.T, tags map[string]string, lat, lon float64) []byte {
	t.Helper()
	b, err := json.Marshal(osmSnapshot{Tags: tags, Lat: lat, Lon: lon})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func changeOf(list []OsmChange, key string) *OsmChange {
	for i := range list {
		if list[i].Key == key {
			return &list[i]
		}
	}
	return nil
}

// What changed in an element is told for the tags that matter, the position when it moved more than a few metres,
// and the element having gone; the rest changes all the time and is not told.
func TestOsmChanges(t *testing.T) {
	seen := snapshot(t, map[string]string{"name": "Cueva A", "ele": "1200", "source": "survey", "natural": "cave_entrance"}, 40.5, -3.7)

	// The same: nothing. And nothing seen: nothing to compare.
	if got := osmChanges(seen, osmSnapshot{Tags: map[string]string{"name": "Cueva A", "ele": "1200", "source": "survey", "natural": "cave_entrance"}, Lat: 40.5, Lon: -3.7}, false); len(got) != 0 {
		t.Errorf("no changes expected: %+v", got)
	}
	if got := osmChanges(nil, osmSnapshot{Tags: map[string]string{"name": "Otra"}}, true); got != nil {
		t.Errorf("nothing seen, nothing told: %+v", got)
	}

	now := osmSnapshot{
		Tags: map[string]string{"name": "Cueva B", "description": "Con agua", "source": "bing", "natural": "cave_entrance"},
		Lat:  40.5, Lon: -3.7,
	}
	got := osmChanges(seen, now, false)
	if name := changeOf(got, "name"); name == nil || *name.Before != "Cueva A" || *name.After != "Cueva B" {
		t.Errorf("a renamed element: %+v", name)
	}
	if ele := changeOf(got, "ele"); ele == nil || *ele.Before != "1200" || ele.After != nil {
		t.Errorf("a tag taken away has no \"after\": %+v", ele)
	}
	if d := changeOf(got, "description"); d == nil || d.Before != nil || *d.After != "Con agua" {
		t.Errorf("a tag added has no \"before\": %+v", d)
	}
	if changeOf(got, "source") != nil || changeOf(got, "position") != nil || changeOf(got, "removed") != nil || len(got) != 3 {
		t.Errorf("only the tags that matter, and no move or removal: %+v", got)
	}

	// 0.0001 degrees of latitude are about 11 metres; 0.00005, about 5.
	moved := osmSnapshot{Tags: seenTags(), Lat: 40.5001, Lon: -3.7}
	if p := changeOf(osmChanges(seen, moved, false), "position"); p == nil || *p.Before != "40.50000, -3.70000" || *p.After != "40.50010, -3.70000" {
		t.Errorf("a move of 11 m: %+v", p)
	}
	almost := osmSnapshot{Tags: seenTags(), Lat: 40.50005, Lon: -3.7}
	if got := osmChanges(seen, almost, false); len(got) != 0 {
		t.Errorf("5 m is not told: %+v", got)
	}

	if g := changeOf(osmChanges(seen, osmSnapshot{Tags: seenTags(), Lat: 40.5, Lon: -3.7}, true), "removed"); g == nil {
		t.Error("an element that is gone is told")
	}
}

func seenTags() map[string]string {
	return map[string]string{"name": "Cueva A", "ele": "1200", "source": "survey", "natural": "cave_entrance"}
}
