// Package library serves a read-only tree of club documents — estatutos,
// reglamentos, normativas, memorias de actividades, actas de reuniones —
// from a directory on disk (Service.root, set once at startup from
// config.LibraryRoot) so every baseline member can browse and download
// them. There's no database involved, so unlike every other resource
// package there's no repository.go/pgx layer: Service works directly
// against the filesystem, resolving and validating every caller-given
// path against root before touching it (see Service.resolve) so a
// request can never read outside the configured tree — the one thing
// that actually needs care here, in place of the usual data-integrity
// concerns a SQL schema would otherwise enforce.
package library

import "time"

type EntryType string

const (
	EntryDir  EntryType = "dir"
	EntryFile EntryType = "file"
)

// Entry is one item inside a listed directory — either a subdirectory to
// browse into or a file to view/download.
type Entry struct {
	Name       string    `json:"name"`
	Type       EntryType `json:"type"`
	Size       int64     `json:"size,omitempty"`
	ModifiedAt time.Time `json:"modified_at"`
}

// Listing is a directory's contents, subdirectories first then files,
// both alphabetical — the shape GET /library returns.
type Listing struct {
	// Path is the same slash-separated, root-relative path that was
	// requested, normalized (see cleanRelPath) — echoed back so the
	// frontend can build breadcrumbs and "up one level" links without
	// having to re-derive it from whatever it originally passed in.
	Path    string  `json:"path"`
	Entries []Entry `json:"entries"`
}
