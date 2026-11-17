package osmmirror

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"
)

// Source is an extract that feeds the mirror: Geofabrik cuts by region, not by country, so Spain is two.
type Source struct {
	Name string // Stored with each element and in the log of loads.
	URL  string // Where to download it from; empty if File says where it is.
	File string // A local `.osm.pbf` (a file already downloaded); used instead of URL.
}

// DefaultSources are the extracts that hold Spain: the Spanish one has the Balearic Islands, Ceuta and
// Melilla, and the Canary Islands are in the African ones.
var DefaultSources = []Source{
	{Name: "spain", URL: "https://download.geofabrik.de/europe/spain-latest.osm.pbf"},
	{Name: "canary-islands", URL: "https://download.geofabrik.de/africa/canary-islands-latest.osm.pbf"},
}

// Extractor reads an extract (Osmium does it).
type Extractor interface {
	Timestamp(ctx context.Context, file string) (time.Time, error)
	Export(ctx context.Context, file string) (path string, cleanup func(), err error)
	ExportRoutes(ctx context.Context, file string) (path string, cleanup func(), err error)
}

// Syncer loads extracts into the mirror.
type Syncer struct {
	Repo      *Repository
	Extractor Extractor
	// MinKept is the part of what a source had that a load has to find for it to be believed: below it
	// the extract is taken for a damaged or a wrong one and nothing is touched, since marking most of the
	// mirror as gone for a download that went wrong would be worse than keeping it as it is.
	MinKept float64
	// MinSmall is how few elements a source can have before that check does not apply.
	MinSmall int
}

// NewSyncer is a syncer that does not accept a load that loses more than half of a source.
func NewSyncer(repo *Repository, extractor Extractor) *Syncer {
	return &Syncer{Repo: repo, Extractor: extractor, MinKept: 0.5, MinSmall: 20}
}

// ErrTooFew means a load found far fewer elements than the source had, see Syncer.MinKept.
var ErrTooFew = errors.New("the extract has far fewer elements than the mirror has of it")

// ErrUpToDate means the extract is the one that was loaded last time.
var ErrUpToDate = errors.New("the extract is the one already loaded")

// Sync loads the extract in `file` as the source `name`. It leaves a row in the log of loads, ok or
// failed, and does nothing (ErrUpToDate) if the data in the file is the date of the last successful load,
// unless force is set.
func (s *Syncer) Sync(ctx context.Context, name, file string, force bool) (Result, error) {
	stamp, err := s.Extractor.Timestamp(ctx, file)
	if err != nil {
		return Result{}, err
	}
	if !force {
		last, err := s.Repo.LastOK(ctx, name)
		if err != nil {
			return Result{}, err
		}
		if last != nil && last.SourceTimestamp != nil && !stamp.After(*last.SourceTimestamp) {
			return Result{}, ErrUpToDate
		}
	}
	runID, err := s.Repo.StartRun(ctx, name, stamp)
	if err != nil {
		return Result{}, err
	}
	res, runErr := s.load(ctx, name, file, force)
	// The log is written even when the context is gone (a stop signal), so a load is never left "running".
	if err := s.Repo.FinishRun(context.WithoutCancel(ctx), runID, res, runErr); err != nil {
		slog.Error("close the row of a load", "error", err, "run_id", runID)
	}
	return res, runErr
}

func (s *Syncer) readRoutes(ctx context.Context, file string) ([]Element, error) {
	path, cleanup, err := s.Extractor.ExportRoutes(ctx, file)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return ReadRoutes(f)
}

func (s *Syncer) load(ctx context.Context, name, file string, force bool) (Result, error) {
	path, cleanup, err := s.Extractor.Export(ctx, file)
	if err != nil {
		return Result{}, err
	}
	defer cleanup()
	f, err := os.Open(path)
	if err != nil {
		return Result{}, err
	}
	defer f.Close()
	elements, err := Read(f)
	if err != nil {
		return Result{}, fmt.Errorf("read the elements of %s: %w", name, err)
	}
	routes, err := s.readRoutes(ctx, file)
	if err != nil {
		return Result{}, fmt.Errorf("read the routes of %s: %w", name, err)
	}
	elements = append(elements, routes...)
	if !force {
		before, err := s.Repo.Active(ctx, name)
		if err != nil {
			return Result{}, err
		}
		if before >= s.MinSmall && float64(len(elements)) < float64(before)*s.MinKept {
			return Result{}, fmt.Errorf("%w: %d found, %d in the mirror (use force if it is right)", ErrTooFew, len(elements), before)
		}
	}
	return s.Repo.Apply(ctx, name, elements)
}
