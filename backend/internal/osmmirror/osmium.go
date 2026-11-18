package osmmirror

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Osmium cuts an extract down to the elements that matter and exports them, with the `osmium` tool
// (osmium-tool). It is the only thing that reads a `.osm.pbf`.
type Osmium struct {
	Binary string // "osmium" if empty.
	TmpDir string // Where the cut and the export go; the system's if empty.
}

func (o Osmium) binary() string {
	if o.Binary == "" {
		return "osmium"
	}
	return o.Binary
}

func (o Osmium) run(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, o.binary(), args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("osmium %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// Timestamp is the date of the data in an extract (its replication timestamp, or else the one of its
// header).
func (o Osmium) Timestamp(ctx context.Context, file string) (time.Time, error) {
	for _, key := range []string{"header.option.osmosis_replication_timestamp", "header.option.timestamp"} {
		out, err := o.run(ctx, "fileinfo", "-g", key, file)
		if err != nil {
			return time.Time{}, err
		}
		if text := strings.TrimSpace(string(out)); text != "" {
			t, err := time.Parse(time.RFC3339, text)
			if err != nil {
				return time.Time{}, fmt.Errorf("the timestamp %q of %s: %w", text, file, err)
			}
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("%s has no timestamp in its header", file)
}

// ExportRoutes cuts the extract down to the route relations of the filters, with their member ways and the
// nodes of those, and writes them as OPL text into a file of its own that the caller removes: `osmium export`
// does not build shapes for routes (see ReadRoutes).
func (o Osmium) ExportRoutes(ctx context.Context, file string) (path string, cleanup func(), err error) {
	dir, err := os.MkdirTemp(o.TmpDir, "osmsync-")
	if err != nil {
		return "", nil, err
	}
	cleanup = func() { _ = os.RemoveAll(dir) }
	cut := filepath.Join(dir, "routes.osm.pbf")
	args := append([]string{"tags-filter", file}, RouteExpressions()...)
	if _, err := o.run(ctx, append(args, "-o", cut)...); err != nil {
		cleanup()
		return "", nil, err
	}
	path = filepath.Join(dir, "routes.opl")
	if _, err := o.run(ctx, "cat", cut, "-f", "opl", "-o", path); err != nil {
		cleanup()
		return "", nil, err
	}
	return path, cleanup, nil
}

// ExportBoundaries writes the boundaries of the municipalities of the extract (the relations with admin_level=8)
// and those of the countries and territories that have an ISO 3166-1 code (Andorra, Gibraltar), as polygons, as
// GeoJSON lines into a file of its own that the caller removes, for AssignZones.
func (o Osmium) ExportBoundaries(ctx context.Context, file string) (path string, cleanup func(), err error) {
	dir, err := os.MkdirTemp(o.TmpDir, "osmsync-")
	if err != nil {
		return "", nil, err
	}
	cleanup = func() { _ = os.RemoveAll(dir) }
	cut := filepath.Join(dir, "boundaries.osm.pbf")
	if _, err := o.run(ctx, "tags-filter", file, "r/admin_level=8", "r/ISO3166-1:alpha2", "-o", cut); err != nil {
		cleanup()
		return "", nil, err
	}
	path = filepath.Join(dir, "boundaries.geojsonseq")
	if _, err := o.run(ctx, "export", cut, "-f", "geojsonseq", "--geometry-types=polygon", "-o", path); err != nil {
		cleanup()
		return "", nil, err
	}
	return path, cleanup, nil
}

// Export cuts the extract with the filters and writes the elements as GeoJSON lines into a file of its own
// that the caller removes (the returned `cleanup`).
func (o Osmium) Export(ctx context.Context, file string) (path string, cleanup func(), err error) {
	dir, err := os.MkdirTemp(o.TmpDir, "osmsync-")
	if err != nil {
		return "", nil, err
	}
	cleanup = func() { _ = os.RemoveAll(dir) }
	cut := filepath.Join(dir, "cut.osm.pbf")
	args := append([]string{"tags-filter", file}, Expressions()...)
	if _, err := o.run(ctx, append(args, "-o", cut)...); err != nil {
		cleanup()
		return "", nil, err
	}
	path = filepath.Join(dir, "elements.geojsonseq")
	if _, err := o.run(ctx, "export", cut, "-f", "geojsonseq", "-a", "type,id,version,timestamp", "-o", path); err != nil {
		cleanup()
		return "", nil, err
	}
	return path, cleanup, nil
}
