package osmmirror

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Routes are relations (`route=canyoning`) that `osmium export` does not turn into a shape: a canyon is a
// list of the ways of its stream, with no polygon to build. They are cut apart and read from the OPL text
// that `osmium cat -f opl` writes (one line per object, with the tags, the nodes of a way and the members of
// a relation), and put together here: the outline is a MultiLineString of the member ways, and the point is
// the middle of those ways laid end to end.

type oplWay struct{ nodes []int64 }

type oplMember struct {
	typ string
	id  int64
}

type oplRelation struct {
	id        int64
	version   int
	timestamp time.Time
	tags      map[string]string
	members   []oplMember
}

// ReadRoutes reads the relations that match a filter out of an OPL file that has them with their member ways
// and the nodes of those, and gives them as elements. A relation with no member way that is in the file has
// no shape and is skipped.
func ReadRoutes(r io.Reader) ([]Element, error) {
	br := bufio.NewReaderSize(r, 1<<20)
	nodes := map[int64][2]float64{}
	ways := map[int64]oplWay{}
	var relations []oplRelation
	for line := 1; ; line++ {
		text, err := br.ReadString('\n')
		text = strings.TrimRight(text, "\r\n")
		if text != "" {
			if parseErr := parseOPL(text, nodes, ways, &relations); parseErr != nil {
				return nil, fmt.Errorf("line %d: %w", line, parseErr)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	var out []Element
	for _, rel := range relations {
		category := Classify(rel.tags)
		if category == "" {
			continue
		}
		var lines [][][2]float64
		for _, m := range rel.members {
			if m.typ != "w" {
				continue
			}
			w, ok := ways[m.id]
			if !ok {
				continue
			}
			var line [][2]float64
			for _, n := range w.nodes {
				if c, ok := nodes[n]; ok {
					line = append(line, c)
				}
			}
			if len(line) >= 2 {
				lines = append(lines, line)
			}
		}
		if len(lines) == 0 {
			continue
		}
		chain := chainLines(lines)
		x, y, ok := lineMiddle(chain)
		if !ok {
			continue
		}
		out = append(out, Element{
			Type: "relation", ID: rel.id, Version: rel.version, Timestamp: rel.timestamp, Category: category,
			Lat: round7(y), Lon: round7(x), Tags: rel.tags, Geometry: multiLineString(lines),
		})
	}
	return out, nil
}

// chainLines lays the lines end to end in their order, turning each one round when its other end is the
// one that meets the line before, so that the middle of the whole is a point on the stream and not a jump
// between two pieces that do not touch.
func chainLines(lines [][][2]float64) [][2]float64 {
	var chain [][2]float64
	for _, line := range lines {
		if len(chain) > 0 {
			last := chain[len(chain)-1]
			if dist(line[0], last) > dist(line[len(line)-1], last) {
				rev := make([][2]float64, len(line))
				for i, c := range line {
					rev[len(line)-1-i] = c
				}
				line = rev
			}
		}
		chain = append(chain, line...)
	}
	return chain
}

func dist(a, b [2]float64) float64 { return math.Hypot(a[0]-b[0], a[1]-b[1]) }

func multiLineString(lines [][][2]float64) map[string]any {
	coords := make([]any, 0, len(lines))
	for _, line := range lines {
		points := make([]any, 0, len(line))
		for _, c := range line {
			points = append(points, []any{c[0], c[1]})
		}
		coords = append(coords, points)
	}
	return map[string]any{"type": "MultiLineString", "coordinates": coords}
}

// parseOPL takes one line of an OPL file (https://osmcode.org/opl-file-format/): the letter of the type and
// the id, then fields that each start with a letter, separated by spaces.
func parseOPL(line string, nodes map[int64][2]float64, ways map[int64]oplWay, relations *[]oplRelation) error {
	fields := strings.Split(line, " ")
	head := fields[0]
	if len(head) < 2 {
		return fmt.Errorf("not an OPL line: %.40q", line)
	}
	id, err := strconv.ParseInt(head[1:], 10, 64)
	if err != nil {
		return fmt.Errorf("bad id in %.40q", line)
	}
	var x, y float64
	var hasX, hasY, deleted bool
	var tags map[string]string
	var nodeRefs []int64
	var members []oplMember
	var version int
	var stamp time.Time
	for _, f := range fields[1:] {
		if f == "" {
			continue
		}
		value := f[1:]
		switch f[0] {
		case 'v':
			version, _ = strconv.Atoi(value)
		case 'd':
			deleted = value == "D"
		case 't':
			stamp, _ = time.Parse(time.RFC3339, value)
		case 'T':
			tags = parseOPLTags(value)
		case 'x':
			x, err = strconv.ParseFloat(value, 64)
			hasX = err == nil
		case 'y':
			y, err = strconv.ParseFloat(value, 64)
			hasY = err == nil
		case 'N':
			for _, s := range strings.Split(value, ",") {
				if n, err := strconv.ParseInt(strings.TrimPrefix(s, "n"), 10, 64); err == nil {
					nodeRefs = append(nodeRefs, n)
				}
			}
		case 'M':
			for _, s := range strings.Split(value, ",") {
				ref, _, _ := strings.Cut(s, "@")
				if len(ref) < 2 {
					continue
				}
				if n, err := strconv.ParseInt(ref[1:], 10, 64); err == nil {
					members = append(members, oplMember{typ: ref[:1], id: n})
				}
			}
		}
	}
	if deleted {
		return nil
	}
	switch head[0] {
	case 'n':
		if hasX && hasY {
			nodes[id] = [2]float64{x, y}
		}
	case 'w':
		ways[id] = oplWay{nodes: nodeRefs}
	case 'r':
		*relations = append(*relations, oplRelation{id: id, version: version, timestamp: stamp.UTC(), tags: tags, members: members})
	}
	return nil
}

// parseOPLTags reads "k=v,k=v" with the escapes of OPL: a character that is not plain is written as its
// code in hexadecimal between two percent signs ("%20%" is a space, "%2c%" a comma).
func parseOPLTags(s string) map[string]string {
	tags := map[string]string{}
	if s == "" {
		return tags
	}
	for _, kv := range strings.Split(s, ",") {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		tags[unescapeOPL(k)] = unescapeOPL(v)
	}
	return tags
}

func unescapeOPL(s string) string {
	if !strings.Contains(s, "%") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] != '%' {
			b.WriteByte(s[i])
			i++
			continue
		}
		end := strings.IndexByte(s[i+1:], '%')
		if end < 0 {
			b.WriteString(s[i:])
			break
		}
		code, err := strconv.ParseUint(s[i+1:i+1+end], 16, 32)
		if err != nil || !utf8.ValidRune(rune(code)) {
			b.WriteString(s[i : i+end+2])
		} else {
			b.WriteRune(rune(code))
		}
		i += end + 2
	}
	return b.String()
}
