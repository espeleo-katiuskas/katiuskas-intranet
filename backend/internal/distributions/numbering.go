package distributions

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

var (
	// ErrInvalidNumbering means a field of an article's numbering with no name or
	// a repeated one, or a spec that isn't a list of values and ranges, or one
	// that makes too many items.
	ErrInvalidNumbering = errors.New("each numbering field needs a distinct name and values like \"124-133, 137\", with at most 100000 items in all")
	// ErrNumberInUse means a change of numbering would leave out a number somebody
	// already has.
	ErrNumberInUse = errors.New("a number somebody already has would no longer exist")
	// ErrNotEnoughNumbers means there are fewer free numbers than the ones to give.
	ErrNotEnoughNumbers = errors.New("there are not enough free numbers")
	// ErrNotNumbered means the article has no numbering to list or assign from.
	ErrNotNumbered = errors.New("this article's numbers are not described")
)

const (
	maxNumberingItems   = 100000
	identifierSeparator = "/"
)

// IdentifierField is one part of the number of an article's items — "Serie",
// "Fracción" — with the values it can take as a spec: values and ranges
// separated by commas, "124-133, 137". A range of digits keeps the width of
// its ends when one has leading zeros ("0001-0200").
type IdentifierField struct {
	Name string `json:"name"`
	Spec string `json:"spec"`
}

// numbering is a described numbering, expanded: the items are every combination
// of one value of each field, the first field changing slowest.
type numbering struct {
	names  []string
	values [][]string
	index  []map[string]int
}

// expandSpec turns "124-133, 137" into its values, in the order written, with
// none repeated.
func expandSpec(spec string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	add := func(v string) error {
		if seen[v] {
			return ErrInvalidNumbering
		}
		seen[v] = true
		out = append(out, v)
		if len(out) > maxNumberingItems {
			return ErrInvalidNumbering
		}
		return nil
	}
	for _, token := range strings.Split(spec, ",") {
		token = strings.TrimSpace(token)
		if token == "" || strings.Contains(token, identifierSeparator) {
			return nil, ErrInvalidNumbering
		}
		from, to, isRange := strings.Cut(token, "-")
		from, to = strings.TrimSpace(from), strings.TrimSpace(to)
		if isRange && allDigits(from) && allDigits(to) {
			a, errA := strconv.Atoi(from)
			b, errB := strconv.Atoi(to)
			if errA != nil || errB != nil || a > b || b-a >= maxNumberingItems {
				return nil, ErrInvalidNumbering
			}
			width := 0
			if (strings.HasPrefix(from, "0") || strings.HasPrefix(to, "0")) && len(from) == len(to) {
				width = len(from)
			}
			for n := a; n <= b; n++ {
				if err := add(fmt.Sprintf("%0*d", width, n)); err != nil {
					return nil, err
				}
			}
			continue
		}
		if err := add(token); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// buildNumbering expands fields, which must be distinct by name and give at
// most maxNumberingItems items together. No fields is no numbering (nil).
func buildNumbering(fields []IdentifierField) (*numbering, error) {
	if len(fields) == 0 {
		return nil, nil
	}
	n := &numbering{}
	names := map[string]bool{}
	total := 1
	for _, f := range fields {
		name := strings.TrimSpace(f.Name)
		if name == "" || names[name] {
			return nil, ErrInvalidNumbering
		}
		names[name] = true
		values, err := expandSpec(f.Spec)
		if err != nil {
			return nil, err
		}
		total *= len(values)
		if total > maxNumberingItems {
			return nil, ErrInvalidNumbering
		}
		index := make(map[string]int, len(values))
		for i, v := range values {
			index[v] = i
		}
		n.names = append(n.names, name)
		n.values = append(n.values, values)
		n.index = append(n.index, index)
	}
	return n, nil
}

func (n *numbering) total() int {
	total := 1
	for _, v := range n.values {
		total *= len(v)
	}
	return total
}

// valid says whether identifier is one of the items: one value of each field,
// joined by identifierSeparator.
func (n *numbering) valid(identifier string) bool {
	parts := strings.Split(identifier, identifierSeparator)
	if len(parts) != len(n.names) {
		return false
	}
	for i, p := range parts {
		if _, ok := n.index[i][p]; !ok {
			return false
		}
	}
	return true
}

// each calls fn with every item in order, until it returns false.
func (n *numbering) each(fn func(identifier string) bool) {
	parts := make([]string, len(n.names))
	var walk func(field int) bool
	walk = func(field int) bool {
		if field == len(n.names) {
			return fn(strings.Join(parts, identifierSeparator))
		}
		for _, v := range n.values[field] {
			parts[field] = v
			if !walk(field + 1) {
				return false
			}
		}
		return true
	}
	walk(0)
}

// expand turns what somebody typed into identifiers: each is a number as it is
// ("124/3"), or its last field may be a range over that field's values,
// "124/1-5" (or "0001-0010" for one field), taken by position in the values, so
// a gap in them is respected.
func (n *numbering) expand(tokens []string) ([]string, error) {
	var out []string
	last := len(n.names) - 1
	for _, token := range tokens {
		token = strings.TrimSpace(token)
		if n.valid(token) {
			out = append(out, token)
			continue
		}
		cut := strings.LastIndex(token, identifierSeparator)
		prefix, rest := "", token
		if cut >= 0 {
			prefix, rest = token[:cut+1], token[cut+1:]
		}
		from, to, isRange := strings.Cut(rest, "-")
		a, okA := n.index[last][strings.TrimSpace(from)]
		b, okB := n.index[last][strings.TrimSpace(to)]
		if !isRange || !okA || !okB || a > b || strings.Count(prefix, identifierSeparator) != last {
			return nil, ErrInvalidIdentifiers
		}
		for _, v := range n.values[last][a : b+1] {
			out = append(out, prefix+v)
		}
	}
	return out, nil
}

// compareNumbers orders two numbers part by part (a number is its parts joined by a
// slash): the parts that are all digits as numbers — "9" before "10", the same
// number with leading zeros shortest first — and any other text as text. The same
// order as the frontend's sortIdentifiers.
func compareNumbers(a, b string) int {
	pa := strings.Split(a, identifierSeparator)
	pb := strings.Split(b, identifierSeparator)
	for i := 0; i < min(len(pa), len(pb)); i++ {
		x, y := pa[i], pb[i]
		if allDigits(x) && allDigits(y) {
			// Compared as numbers by length first (no leading zeros) then by digits,
			// which holds for any length without overflowing.
			sx, sy := strings.TrimLeft(x, "0"), strings.TrimLeft(y, "0")
			if len(sx) != len(sy) {
				return len(sx) - len(sy)
			}
			if c := strings.Compare(sx, sy); c != 0 {
				return c
			}
			if len(x) != len(y) {
				return len(x) - len(y)
			}
			continue
		}
		if c := compareText(x, y); c != 0 {
			return c
		}
	}
	return len(pa) - len(pb)
}

// compareText orders text with the digits in it as numbers ("A-9" before "A-10"),
// ignoring case.
func compareText(x, y string) int {
	x, y = strings.ToLower(x), strings.ToLower(y)
	for x != "" && y != "" {
		if allDigits(x[:1]) && allDigits(y[:1]) {
			dx, dy := leadingDigits(x), leadingDigits(y)
			if c := compareNumbers(dx, dy); c != 0 {
				return c
			}
			x, y = x[len(dx):], y[len(dy):]
			continue
		}
		if x[0] != y[0] {
			return int(x[0]) - int(y[0])
		}
		x, y = x[1:], y[1:]
	}
	return len(x) - len(y)
}

// leadingDigits is the digits a string starts with.
func leadingDigits(s string) string {
	n := 0
	for n < len(s) && s[n] >= '0' && s[n] <= '9' {
		n++
	}
	return s[:n]
}

// formatNumbers says a list of numbers as short as it can be, for a message: in
// order, with the consecutive ones joined into ranges ("0001-0003, 0010",
// "124/1-10, 125/1-2"), the same as the frontend shows them.
func formatNumbers(identifiers []string) string {
	sorted := slices.Clone(identifiers)
	slices.SortFunc(sorted, compareNumbers)
	type run struct{ prefix, from, to string }
	var runs []run
	for _, id := range sorted {
		cut := strings.LastIndex(id, identifierSeparator)
		prefix, last := "", id
		if cut >= 0 {
			prefix, last = id[:cut], id[cut+1:]
		}
		if n := len(runs); n > 0 {
			r := &runs[n-1]
			if r.prefix == prefix && allDigits(last) && allDigits(r.to) &&
				(len(last) == len(r.to) || (!strings.HasPrefix(last, "0") && !strings.HasPrefix(r.to, "0"))) &&
				isNext(r.to, last) {
				r.to = last
				continue
			}
		}
		runs = append(runs, run{prefix, last, last})
	}
	parts := make([]string, len(runs))
	for i, r := range runs {
		text := r.from
		if r.from != r.to {
			text = r.from + "-" + r.to
		}
		if r.prefix != "" {
			text = r.prefix + identifierSeparator + text
		}
		parts[i] = text
	}
	return strings.Join(parts, ", ")
}

// isNext says whether next is the number after prev, both only digits.
func isNext(prev, next string) bool {
	a, errA := strconv.Atoi(prev)
	b, errB := strconv.Atoi(next)
	return errA == nil && errB == nil && b == a+1
}

// withoutThose is list without the numbers that are in take away, in its order.
func withoutThose(list, takeAway []string) []string {
	var out []string
	for _, identifier := range list {
		if !slices.Contains(takeAway, identifier) {
			out = append(out, identifier)
		}
	}
	return out
}
