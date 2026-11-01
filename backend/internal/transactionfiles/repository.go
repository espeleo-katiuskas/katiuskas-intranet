package transactionfiles

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/platform/db"
)

type Repository struct {
	pool db.PGX
}

func NewRepository(pool db.PGX) *Repository {
	return &Repository{pool: pool}
}

// fileSelect is the metadata of a file, with the login of whoever attached it.
const fileSelect = `
	SELECT f.id, f.transaction_id, f.file_name, f.content_type, f.file_size, u.login, f.created_at
	FROM transaction_files f LEFT JOIN users u ON u.id = f.uploaded_by`

func scanFile(row pgx.Row, f *File) error {
	return row.Scan(&f.ID, &f.TransactionID, &f.FileName, &f.ContentType, &f.FileSize, &f.UploadedBy, &f.CreatedAt)
}

// TransactionDescription is the description of a transaction, or ErrTransactionNotFound.
func (r *Repository) TransactionDescription(ctx context.Context, id int64) (string, error) {
	var description string
	err := r.pool.QueryRow(ctx, `SELECT description FROM transactions WHERE id = $1`, id).Scan(&description)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrTransactionNotFound
	}
	return description, err
}

func (r *Repository) Create(ctx context.Context, transactionID int64, uploadedBy int64, name, contentType string, size int) (File, error) {
	var id int64
	err := r.pool.QueryRow(ctx, `
		INSERT INTO transaction_files (transaction_id, file_name, content_type, file_size, uploaded_by)
		VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		transactionID, name, contentType, size, uploadedBy).Scan(&id)
	if err != nil {
		return File{}, err
	}
	var f File
	err = scanFile(r.pool.QueryRow(ctx, fileSelect+` WHERE f.id = $1`, id), &f)
	return f, err
}

func (r *Repository) ListByTransaction(ctx context.Context, transactionID int64) ([]File, error) {
	rows, err := r.pool.Query(ctx, fileSelect+` WHERE f.transaction_id = $1 ORDER BY f.id`, transactionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []File{}
	for rows.Next() {
		var f File
		if err := scanFile(rows, &f); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// Get is a file's metadata, which has to belong to the transaction.
func (r *Repository) Get(ctx context.Context, transactionID, id int64) (File, error) {
	var f File
	err := scanFile(r.pool.QueryRow(ctx, fileSelect+` WHERE f.id = $1 AND f.transaction_id = $2`, id, transactionID), &f)
	if errors.Is(err, pgx.ErrNoRows) {
		return File{}, ErrNotFound
	}
	return f, err
}

func (r *Repository) Delete(ctx context.Context, transactionID, id int64) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM transaction_files WHERE id = $1 AND transaction_id = $2`, id, transactionID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// List is every file with the details of its transaction, the transaction most recent first.
func (r *Repository) List(ctx context.Context, filter ListFilter) ([]Listing, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT f.id, f.transaction_id, f.file_name, f.content_type, f.file_size, u.login, f.created_at,
		       t.description, t.occurred_at
		FROM transaction_files f
		JOIN transactions t ON t.id = f.transaction_id
		LEFT JOIN users u ON u.id = f.uploaded_by
		WHERE ($1::int IS NULL OR (t.occurred_at >= make_timestamptz($1, 1, 1, 0, 0, 0, 'UTC')
		                       AND t.occurred_at < make_timestamptz($1 + 1, 1, 1, 0, 0, 0, 'UTC')))
		ORDER BY t.occurred_at DESC, t.id DESC, f.id
		LIMIT $2 OFFSET $3`, filter.Year, filter.Limit, filter.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []Listing{}
	ids := []int64{}
	seen := map[int64]bool{}
	for rows.Next() {
		var l Listing
		if err := rows.Scan(&l.ID, &l.TransactionID, &l.FileName, &l.ContentType, &l.FileSize, &l.UploadedBy, &l.CreatedAt,
			&l.Transaction.Description, &l.Transaction.OccurredAt); err != nil {
			return nil, err
		}
		l.Transaction.ID = l.TransactionID
		list = append(list, l)
		if !seen[l.TransactionID] {
			seen[l.TransactionID] = true
			ids = append(ids, l.TransactionID)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return list, nil
	}
	entryRows, err := r.pool.Query(ctx, `
		SELECT e.transaction_id, e.account_id, a.name, e.amount::float8
		FROM transaction_entries e JOIN accounts a ON a.id = e.account_id
		WHERE e.transaction_id = ANY($1) ORDER BY e.transaction_id, e.id`, ids)
	if err != nil {
		return nil, err
	}
	defer entryRows.Close()
	byTransaction := map[int64][]EntryRef{}
	for entryRows.Next() {
		var transactionID int64
		var e EntryRef
		if err := entryRows.Scan(&transactionID, &e.AccountID, &e.AccountName, &e.Amount); err != nil {
			return nil, err
		}
		byTransaction[transactionID] = append(byTransaction[transactionID], e)
	}
	if err := entryRows.Err(); err != nil {
		return nil, err
	}
	for i := range list {
		entries := byTransaction[list[i].TransactionID]
		if entries == nil {
			entries = []EntryRef{}
		}
		list[i].Transaction.Entries = entries
		for _, e := range entries {
			if e.Amount > 0 {
				list[i].Transaction.Amount += e.Amount
			}
		}
	}
	return list, nil
}
