package distributions

import (
	"errors"
	"fmt"
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
