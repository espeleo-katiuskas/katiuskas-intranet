// Package transactionfiles holds the documents (tickets, invoices) attached to accounting
// transactions: the metadata in Postgres (migration 00040_transaction_files.sql) and the bytes on
// disk (TRANSACTION_FILES_ROOT), like the files of an activity's memoria.
//
// Everybody who can sign in sees that a transaction has files (accounting's file_count); reading
// the files, and the list of all of them, is for admin, board and money, because a ticket or an
// invoice can carry personal or bank details; attaching and removing is admin and money, like every
// other write of accounting.
package transactionfiles

import (
	"errors"
	"time"
)

// MaxFileSize caps one file: generous for a photographed ticket or a scanned invoice.
const MaxFileSize = 10 << 20 // 10 MiB

var (
	ErrNotFound            = errors.New("file not found")
	ErrTransactionNotFound = errors.New("transaction not found")
	ErrEmptyFile           = errors.New("the file is empty")
)

// File is a file's metadata (never its bytes, except through Open).
type File struct {
	ID            int64     `json:"id"`
	TransactionID int64     `json:"transaction_id"`
	FileName      string    `json:"file_name"`
	ContentType   string    `json:"content_type"`
	FileSize      int64     `json:"file_size"`
	UploadedBy    *string   `json:"uploaded_by,omitempty"` // a login; absent if the person was deleted
	CreatedAt     time.Time `json:"created_at"`
}

// EntryRef is one entry of the transaction a file belongs to, with its account's name.
type EntryRef struct {
	AccountID   int64   `json:"account_id"`
	AccountName string  `json:"account_name"`
	Amount      float64 `json:"amount"`
}

// TransactionRef is what the overview says about the transaction a file belongs to. Amount is
// what the transaction moved (the positive side of its entries).
type TransactionRef struct {
	ID          int64      `json:"id"`
	Description string     `json:"description"`
	OccurredAt  time.Time  `json:"occurred_at"`
	Amount      float64    `json:"amount"`
	Entries     []EntryRef `json:"entries"`
}

// Listing is a file together with its transaction, for GET /transaction-files.
type Listing struct {
	File
	Transaction TransactionRef `json:"transaction"`
}

// ListFilter pages the overview and can narrow it to the transactions of one calendar year.
type ListFilter struct {
	Year   *int
	Limit  int
	Offset int
}
