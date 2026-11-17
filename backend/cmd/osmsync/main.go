// Command osmsync loads the elements of OpenStreetMap that matter to the club (caves, huts, via ferratas)
// into the local mirror, `osm_elements` (docs/osm-integration.md).
//
// For each extract it downloads the file from Geofabrik if it changed (a conditional request, so a file that
// did not change costs no bytes), cuts it with `osmium` to the elements that match the filters, and makes the
// mirror say what the extract says: new elements added, changed ones updated, the ones that are gone marked
// as such. Nothing else asks OpenStreetMap anything. Run it from a timer (deploy/systemd), not from a request.
//
//	osmsync                                     the extracts of Spain, downloaded into OSM_CACHE_DIR
//	osmsync -source spain=/data/spain.osm.pbf   an extract already on disk (named as in the log of loads)
//	osmsync -source name=https://…/x.osm.pbf    another extract
//
// It needs DATABASE_URL and the `osmium` tool (OSMIUM_BIN if it is not in the path).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/osmmirror"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/platform/db"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/platform/logging"
)

type sourceFlags []osmmirror.Source

func (s *sourceFlags) String() string { return fmt.Sprint(*s) }

func (s *sourceFlags) Set(value string) error {
	name, where, ok := strings.Cut(value, "=")
	if !ok || name == "" || where == "" {
		return fmt.Errorf("%q: expected name=url or name=file", value)
	}
	src := osmmirror.Source{Name: name}
	if strings.HasPrefix(where, "http://") || strings.HasPrefix(where, "https://") {
		src.URL = where
	} else {
		src.File = where
	}
	*s = append(*s, src)
	return nil
}

func main() {
	slog.SetDefault(logging.New())
	var sources sourceFlags
	flag.Var(&sources, "source", "an extract, name=url or name=file (repeatable; replaces the default ones)")
	cacheDir := flag.String("cache", os.Getenv("OSM_CACHE_DIR"), "where the downloaded extracts are kept (OSM_CACHE_DIR)")
	force := flag.Bool("force", false, "load even if the extract is the one loaded last time or has far fewer elements")
	flag.Parse()
	if len(sources) == 0 {
		sources = osmmirror.DefaultSources
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		fmt.Fprintln(os.Stderr, "DATABASE_URL is required")
		os.Exit(1)
	}
	pool, err := db.NewPool(ctx, databaseURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect to database: %v\n", err)
		os.Exit(1)
	}
	defer pool.Close()

	if *cacheDir == "" {
		*cacheDir = "osm-cache"
	}
	// Downloads and the temporary files of the cuts go here, whether or not anything is downloaded.
	if err := os.MkdirAll(*cacheDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "create %s: %v\n", *cacheDir, err)
		os.Exit(1)
	}
	syncer := osmmirror.NewSyncer(osmmirror.NewRepository(pool),
		osmmirror.Osmium{Binary: os.Getenv("OSMIUM_BIN"), TmpDir: *cacheDir})
	client := &http.Client{Timeout: 30 * time.Minute}

	failed := 0
	for _, src := range sources {
		if err := run(ctx, syncer, client, src, *cacheDir, *force); err != nil {
			slog.Error("load an extract", "source", src.Name, "error", err)
			failed++
		}
	}
	if failed > 0 {
		os.Exit(1)
	}
}

func run(ctx context.Context, syncer *osmmirror.Syncer, client *http.Client, src osmmirror.Source, cacheDir string, force bool) error {
	file := src.File
	if file == "" {
		path, fresh, err := osmmirror.Download(ctx, client, src.URL, cacheDir, src.Name+".osm.pbf")
		if err != nil {
			return err
		}
		slog.Info("extract", "source", src.Name, "downloaded", fresh, "file", path)
		file = path
	}
	res, err := syncer.Sync(ctx, src.Name, file, force)
	switch {
	case errors.Is(err, osmmirror.ErrUpToDate):
		slog.Info("nothing new", "source", src.Name)
		return nil
	case err != nil:
		return err
	}
	slog.Info("loaded", "source", src.Name, "added", res.Added, "changed", res.Changed,
		"deleted", res.Deleted, "unchanged", res.Unchanged)
	return nil
}
