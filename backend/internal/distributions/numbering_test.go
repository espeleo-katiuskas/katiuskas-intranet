package distributions

import (
	"slices"
	"testing"
)

// The same cases the frontend's sortIdentifiers/formatIdentifiers were checked with:
// the messages must say numbers the way the pages do.
func TestFormatNumbersSortsAndJoinsRanges(t *testing.T) {
	for name, c := range map[string]struct {
		in   []string
		want string
	}{
		"unsorted, one part":  {[]string{"0005", "0001", "0003", "0002", "0004", "0010", "0009"}, "0001-0005, 0009-0010"},
		"numeric, not text":   {[]string{"10", "9", "2", "11", "1"}, "1-2, 9-11"},
		"several parts":       {[]string{"125/2", "124/10", "124/1", "125/1", "124/2", "137/1", "124/9"}, "124/1-2, 124/9-10, 125/1-2, 137/1"},
		"a gap":               {[]string{"0001", "0003", "0002", "0007"}, "0001-0003, 0007"},
		"one":                 {[]string{"0042"}, "0042"},
		"none":                {nil, ""},
		"two next to another": {[]string{"0002", "0001"}, "0001-0002"},
		"width changes":       {[]string{"099", "0100"}, "099, 0100"}, // zero padded to different widths: not the same kind of number
		"free text":           {[]string{"B-2", "A-10", "A-9"}, "A-9, A-10, B-2"},
		"very long digits":    {[]string{"123456789012345678901", "123456789012345678900"}, "123456789012345678900, 123456789012345678901"},
	} {
		got := formatNumbers(c.in)
		if got != c.want {
			t.Errorf("%s: expected %q, got %q", name, c.want, got)
		}
	}
}

func TestCompareNumbersKeepsLeadingZerosShortestFirst(t *testing.T) {
	got := []string{"010", "10", "9", "0009"}
	slices.SortFunc(got, compareNumbers)
	if want := []string{"9", "0009", "10", "010"}; !slices.Equal(got, want) {
		t.Errorf("expected %v, got %v", want, got)
	}
}

func TestWithoutThose(t *testing.T) {
	got := withoutThose([]string{"1", "2", "3", "4"}, []string{"2", "4", "9"})
	if want := []string{"1", "3"}; !slices.Equal(got, want) {
		t.Errorf("expected %v, got %v", want, got)
	}
}
