// Package activityresources manages an activity's memoria (write-up):
// an ordered list of resources contributed after the trip by whoever
// took part in it. A resource is either a text entry (a title plus a
// plain-text or Markdown description) or an uploaded file (a title plus
// the file itself, typically a photo or a PDF) — see ResourceType.
// Metadata lives in Postgres (this package's own repository.go); a
// file resource's bytes live on disk instead (Storage, in storage.go),
// keyed by this row's own id rather than any caller-given path, so —
// unlike internal/library or internal/userfiles — there's no directory
// tree to browse and no path-escape risk to guard against.
package activityresources

import "time"

type ResourceType string

const (
	ResourceText ResourceType = "text"
	ResourceFile ResourceType = "file"
)

// Resource is one memoria entry. AuthorID/AuthorLogin is whoever
// contributed it — not necessarily the activity's own organizer — and
// is the only person (besides admin/board) allowed to edit or delete it
// afterward (see Handler.canEdit). Description is set only for a text
// resource; FileName/FileSize/ContentType only for a file one — kept in
// sync with Type by CHECK constraints in the database (migration
// 00010_activity_resources.sql), not just application code.
type Resource struct {
	ID          int64        `json:"id"`
	ActivityID  int64        `json:"activity_id"`
	Type        ResourceType `json:"type"`
	AuthorID    int64        `json:"-"`
	AuthorLogin string       `json:"author_login"`
	Title       string       `json:"title"`
	Description *string      `json:"description,omitempty"`
	FileName    *string      `json:"file_name,omitempty"`
	FileSize    *int64       `json:"file_size,omitempty"`
	ContentType *string      `json:"content_type,omitempty"`
	// Position is this resource's place in the memoria page, always a
	// dense 1..N per activity — see Repository.Reorder.
	Position int32 `json:"position"`
	// CreatedAt is fixed the moment this resource is added and never
	// changes afterward — there's no "edited_at", and no way to backdate
	// it to when a photo was actually taken rather than uploaded.
	CreatedAt time.Time `json:"created_at"`
}

// CreateInput is shared by both resource types — exactly which fields
// apply is dictated by Type (see Resource's own doc comment); the
// database CHECK constraints are the actual source of truth for that,
// not this struct's shape.
type CreateInput struct {
	ActivityID  int64
	Type        ResourceType
	AuthorID    int64
	Title       string
	Description *string
	FileName    *string
	FileSize    *int64
	ContentType *string
}

// UpdateInput edits a resource's title (either type) and description
// (text type only — attempting to set one on a file resource is
// rejected, see ErrDescriptionNotAllowed). There's no way to replace a
// file resource's actual bytes; delete it and add a new one instead.
type UpdateInput struct {
	Title       *string
	Description *string
}
