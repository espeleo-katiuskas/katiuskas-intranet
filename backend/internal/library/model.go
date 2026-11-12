// Package library is the club's library: a catalogue of its items (books, magazines, maps,
// topographies…, each row one copy; not the archive of documents, which is internal/archive), their loans to members, the comments the members write
// about them and the files attached to them — a cover and any other resource. The data is in Postgres
// (migration 00058_library.sql) and the bytes of the files on disk (LIBRARY_FILES_ROOT), like the files of
// an activity's memoria.
//
// Who can do what: every member (and board and admin) reads everything about an item, including
// whether it is available, but not who has it, and adds comments and files; admin, board and the
// `biblioteca` role create and change items and their cover; admin, board, `biblioteca` and
// `material` read the loans and record them and their returns. Whatever `biblioteca` can do, board can
// do too.
package library

import (
	"errors"
	"time"
)

// MaxFileSize caps one file: generous for a scanned topography or a photographed page.
const MaxFileSize = 10 << 20 // 10 MiB

// MaxTypeNameLength is the longest name a type may have, in characters.
const MaxTypeNameLength = 60

// MaxCommentLength is the most a comment may say, in characters.
const MaxCommentLength = 2000

var (
	ErrNotFound         = errors.New("item not found")
	ErrCommentNotFound  = errors.New("comment not found")
	ErrResourceNotFound = errors.New("resource not found")
	ErrCoverNotFound    = errors.New("the item has no cover")
	ErrLoanNotFound     = errors.New("loan not found")
	ErrBlankTitle       = errors.New("the title is required")
	ErrTypeRequired     = errors.New("the type is required")
	ErrUnknownType      = errors.New("there is no such type")
	ErrTypeNotFound     = errors.New("type not found")
	ErrBlankTypeName    = errors.New("the name of the type is required")
	ErrTypeNameTooLong  = errors.New("the name of the type is too long")
	ErrDuplicateType    = errors.New("there is already a type with that name")
	ErrTypeInUse        = errors.New("the type has items and cannot be deleted")
	ErrInvalidYear      = errors.New("the year must be between 1000 and 2200")
	ErrBlankComment     = errors.New("the comment is empty")
	ErrCommentTooLong   = errors.New("the comment is too long")
	ErrEmptyFile        = errors.New("the file is empty")
	ErrNotAnImage       = errors.New("the cover has to be an image")
	// ErrItemHasLoans means an item that has been lent cannot be deleted: its history of loans stays.
	ErrItemHasLoans = errors.New("the item has been lent and cannot be deleted")
	// ErrNotAvailable means the item is out: it has a loan that has not been returned.
	ErrNotAvailable = errors.New("the item is not available: it is out on loan")
	// ErrAlreadyReturned means the loan has already been closed.
	ErrAlreadyReturned = errors.New("the loan has already been returned")
	// ErrRecipientNotMember means an item can only be lent to somebody who is a member now.
	ErrRecipientNotMember = errors.New("an item can only be lent to a member")
	// ErrInvalidDate means a date that is not YYYY-MM-DD, and ErrInvalidDates a loan that starts in the
	// future or a return before the loan started or in the future.
	ErrInvalidDate  = errors.New("dates must be YYYY-MM-DD")
	ErrInvalidDates = errors.New("the dates of the loan are not valid")
	// ErrNotYours means somebody tried to remove a comment or a file that somebody else wrote, being
	// neither admin, board nor biblioteca.
	ErrNotYours = errors.New("only who added it, or whoever manages the library, can remove it")
	// ErrTitleTooLong is a caption of a file longer than MaxResourceTitleLength.
	ErrTitleTooLong = errors.New("the title of the file is too long")
)

// MaxResourceTitleLength is the longest caption a file may have, in characters.
const MaxResourceTitleLength = 200

// Item is one copy of an item. It says whether it is available but never who has it: that is in the
// loans, which only whoever manages them reads.
type Item struct {
	ID          int64     `json:"id"`
	TypeID      int64     `json:"type_id"`
	Type        string    `json:"type"`
	Title       string    `json:"title"`
	Author      *string   `json:"author,omitempty"`
	Publisher   *string   `json:"publisher,omitempty"`
	Year        *int      `json:"year,omitempty"`
	ISBN        *string   `json:"isbn,omitempty"`
	Location    *string   `json:"location,omitempty"`
	Description *string   `json:"description,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	// Available is false while the item is out on loan.
	Available bool `json:"available"`
	// HasCover says whether the item has a cover, which is served at GET /library/items/{id}/cover.
	HasCover bool `json:"has_cover"`
	// CommentCount and ResourceCount are the comments and the files that are not the cover.
	CommentCount  int `json:"comment_count"`
	ResourceCount int `json:"resource_count"`
}

// Input is what creating or editing an item takes: every field the person can change (an edit
// replaces them all).
type Input struct {
	TypeID      int64
	Title       string
	Author      *string
	Publisher   *string
	Year        *int
	ISBN        *string
	Location    *string
	Description *string
}

// Type is a kind of item (book, magazine, map…): a catalogue that whoever manages the library edits.
// ItemCount is how many items have it.
type Type struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	ItemCount int    `json:"item_count"`
}

// Comment is a comment on an item, by login: the page looks the name up in the directory.
type Comment struct {
	ID        int64     `json:"id"`
	ItemID    int64     `json:"item_id"`
	UserLogin string    `json:"user_login"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

// Resource is the metadata of a file attached to an item (never its bytes, except through Open).
type Resource struct {
	ID          int64     `json:"id"`
	ItemID      int64     `json:"item_id"`
	IsCover     bool      `json:"is_cover"`
	Title       *string   `json:"title,omitempty"`
	FileName    string    `json:"file_name"`
	ContentType string    `json:"content_type"`
	FileSize    int64     `json:"file_size"`
	UploadedBy  string    `json:"uploaded_by"` // a login
	CreatedAt   time.Time `json:"created_at"`
}

// Loan is a loan of an item to a member. ReturnedAt is absent while the item is out.
type Loan struct {
	ID              int64     `json:"id"`
	ItemID          int64     `json:"item_id"`
	ItemTitle       string    `json:"item_title"`
	UserLogin       string    `json:"user_login"`
	LentAt          string    `json:"lent_at"` // a date, YYYY-MM-DD
	ReturnedAt      *string   `json:"returned_at,omitempty"`
	RecordedByLogin string    `json:"recorded_by_login"`
	Notes           *string   `json:"notes,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}
