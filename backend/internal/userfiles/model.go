// Package userfiles serves a read-only, per-person tree of personal
// documents (a scanned DNI, a signed waiver, medical paperwork...) from
// a directory on disk (Service.root, set once at startup from
// config.UserFilesRoot), one subdirectory per login. Structurally this
// is internal/library's exact model — no database involved, Service
// works directly against the filesystem, resolving and validating
// every caller-given path against root before touching it (see
// Service.resolve) — except every path is additionally scoped under
// the given login's own subtree, and a login with no subtree of its
// own yet (most people, until someone drops a file in for them) is an
// empty listing rather than a 404. Unlike library, this isn't open to
// every baseline role: a person's files are personal, so only that
// person and admin/board may ever see them (see Handler).
package userfiles

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
// both alphabetical — the shape GET /users/{login}/files returns.
type Listing struct {
	// Path is the same slash-separated path that was requested,
	// root-relative *within this person's own subtree* (never including
	// the login itself), normalized (see cleanRelPath) — echoed back so
	// the frontend can build breadcrumbs and "up one level" links
	// without having to re-derive it from whatever it originally passed
	// in.
	Path    string  `json:"path"`
	Entries []Entry `json:"entries"`
}
