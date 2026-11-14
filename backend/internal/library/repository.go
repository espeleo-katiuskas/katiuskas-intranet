package library

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/platform/db"
)

const (
	uniqueViolationCode     = "23505"
	foreignKeyViolationCode = "23503"
)

type Repository struct {
	pool db.PGX
}

func NewRepository(pool db.PGX) *Repository {
	return &Repository{pool: pool}
}

// itemSelect is an item with what is worked out from the rest: whether it is available (it has no
// loan that is still open), whether it has a cover and how many comments and files it has.
const itemSelect = `
	SELECT b.id, b.type_id, t.name, b.title, b.author, b.publisher, b.year, b.isbn, b.location, b.description,
		b.acquired_on::text, b.ended_on::text, b.end_reason, b.created_at,
		b.ended_on IS NULL AND NOT EXISTS (SELECT 1 FROM library_loans l WHERE l.item_id = b.id AND l.returned_at IS NULL),
		EXISTS (SELECT 1 FROM library_resources r WHERE r.item_id = b.id AND r.is_cover),
		(SELECT count(*) FROM library_comments c WHERE c.item_id = b.id),
		(SELECT count(*) FROM library_resources r WHERE r.item_id = b.id AND NOT r.is_cover)
	FROM library_items b JOIN library_types t ON t.id = b.type_id`

func scanItem(row pgx.Row, b *Item) error {
	return row.Scan(&b.ID, &b.TypeID, &b.Type, &b.Title, &b.Author, &b.Publisher, &b.Year, &b.ISBN, &b.Location, &b.Description,
		&b.AcquiredOn, &b.EndedOn, &b.EndReason, &b.CreatedAt, &b.Available, &b.HasCover, &b.CommentCount, &b.ResourceCount)
}

// List is every item, by title.
func (r *Repository) List(ctx context.Context) ([]Item, error) {
	rows, err := r.pool.Query(ctx, itemSelect+` ORDER BY lower(b.title), b.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []Item{}
	for rows.Next() {
		var b Item
		if err := scanItem(rows, &b); err != nil {
			return nil, err
		}
		list = append(list, b)
	}
	return list, rows.Err()
}

func (r *Repository) Get(ctx context.Context, id int64) (Item, error) {
	var b Item
	err := scanItem(r.pool.QueryRow(ctx, itemSelect+` WHERE b.id = $1`, id), &b)
	if errors.Is(err, pgx.ErrNoRows) {
		return Item{}, ErrNotFound
	}
	return b, err
}

func (r *Repository) Create(ctx context.Context, in Input) (Item, error) {
	var id int64
	err := r.pool.QueryRow(ctx, `
		INSERT INTO library_items (type_id, title, author, publisher, year, isbn, location, description,
			acquired_on, ended_on, end_reason)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9::date, $10::date, $11) RETURNING id
	`, in.TypeID, in.Title, in.Author, in.Publisher, in.Year, in.ISBN, in.Location, in.Description,
		in.AcquiredOn, in.EndedOn, in.EndReason).Scan(&id)
	if isForeignKeyViolation(err) {
		return Item{}, ErrUnknownType
	}
	if err != nil {
		return Item{}, err
	}
	return r.Get(ctx, id)
}

// Update replaces every field of an item. In one transaction, with the row locked (so that nobody
// lends it meanwhile), it checks the dates against the loans: the item cannot have been acquired after
// its first loan or left before its last movement, and it cannot leave while it is out on loan.
func (r *Repository) Update(ctx context.Context, id int64, in Input) (Item, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Item{}, err
	}
	defer tx.Rollback(ctx)
	var locked int64
	err = tx.QueryRow(ctx, `SELECT id FROM library_items WHERE id = $1 FOR UPDATE`, id).Scan(&locked)
	if errors.Is(err, pgx.ErrNoRows) {
		return Item{}, ErrNotFound
	}
	if err != nil {
		return Item{}, err
	}
	var out, afterFirstLoan, beforeLastMovement bool
	err = tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM library_loans WHERE item_id = $1 AND returned_at IS NULL),
			COALESCE($2::date > (SELECT min(lent_at) FROM library_loans WHERE item_id = $1), false),
			COALESCE($3::date < (SELECT max(GREATEST(lent_at, returned_at)) FROM library_loans WHERE item_id = $1), false)
	`, id, in.AcquiredOn, in.EndedOn).Scan(&out, &afterFirstLoan, &beforeLastMovement)
	if err != nil {
		return Item{}, err
	}
	if afterFirstLoan || beforeLastMovement {
		return Item{}, ErrInvalidItemDates
	}
	if out && in.EndedOn != nil {
		return Item{}, ErrItemOnLoan
	}
	_, err = tx.Exec(ctx, `
		UPDATE library_items SET type_id = $2, title = $3, author = $4, publisher = $5, year = $6, isbn = $7,
			location = $8, description = $9, acquired_on = $10::date, ended_on = $11::date, end_reason = $12
		WHERE id = $1
	`, id, in.TypeID, in.Title, in.Author, in.Publisher, in.Year, in.ISBN, in.Location, in.Description,
		in.AcquiredOn, in.EndedOn, in.EndReason)
	if isForeignKeyViolation(err) {
		return Item{}, ErrUnknownType
	}
	if err != nil {
		return Item{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Item{}, err
	}
	return r.Get(ctx, id)
}

// Delete removes an item that has never been lent, with its comments and the rows of its files, and
// answers with the ids of those files so that the caller can remove their bytes. Nothing cascades in
// the database, so everything that hangs from the item is deleted here, in one transaction.
func (r *Repository) Delete(ctx context.Context, id int64) ([]int64, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	// The row is locked so that nobody lends it, comments on it or attaches a file while this decides.
	var locked int64
	err = tx.QueryRow(ctx, `SELECT id FROM library_items WHERE id = $1 FOR UPDATE`, id).Scan(&locked)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var lent bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM library_loans WHERE item_id = $1)`, id).Scan(&lent); err != nil {
		return nil, err
	}
	if lent {
		return nil, ErrItemHasLoans
	}
	if _, err := tx.Exec(ctx, `DELETE FROM library_comments WHERE item_id = $1`, id); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `DELETE FROM library_resources WHERE item_id = $1 RETURNING id`, id)
	if err != nil {
		return nil, err
	}
	var files []int64
	for rows.Next() {
		var fileID int64
		if err := rows.Scan(&fileID); err != nil {
			rows.Close()
			return nil, err
		}
		files = append(files, fileID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM library_items WHERE id = $1`, id); err != nil {
		return nil, err
	}
	return files, tx.Commit(ctx)
}

// ----- comments -----

const commentSelect = `
	SELECT c.id, c.item_id, u.login, c.body, c.created_at
	FROM library_comments c JOIN users u ON u.id = c.user_id`

func scanComment(row pgx.Row, c *Comment) error {
	return row.Scan(&c.ID, &c.ItemID, &c.UserLogin, &c.Body, &c.CreatedAt)
}

func (r *Repository) ListComments(ctx context.Context, itemID int64) ([]Comment, error) {
	rows, err := r.pool.Query(ctx, commentSelect+` WHERE c.item_id = $1 ORDER BY c.created_at, c.id`, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []Comment{}
	for rows.Next() {
		var c Comment
		if err := scanComment(rows, &c); err != nil {
			return nil, err
		}
		list = append(list, c)
	}
	return list, rows.Err()
}

func (r *Repository) AddComment(ctx context.Context, itemID, userID int64, body string) (Comment, error) {
	var id int64
	if err := r.pool.QueryRow(ctx, `
		INSERT INTO library_comments (item_id, user_id, body) VALUES ($1, $2, $3) RETURNING id
	`, itemID, userID, body).Scan(&id); err != nil {
		return Comment{}, err
	}
	var c Comment
	err := scanComment(r.pool.QueryRow(ctx, commentSelect+` WHERE c.id = $1`, id), &c)
	return c, err
}

// GetComment is a comment of the item and the id of whoever wrote it.
func (r *Repository) GetComment(ctx context.Context, itemID, id int64) (Comment, int64, error) {
	var c Comment
	var authorID int64
	err := r.pool.QueryRow(ctx, `
		SELECT c.id, c.item_id, u.login, c.body, c.created_at, c.user_id
		FROM library_comments c JOIN users u ON u.id = c.user_id
		WHERE c.item_id = $1 AND c.id = $2
	`, itemID, id).Scan(&c.ID, &c.ItemID, &c.UserLogin, &c.Body, &c.CreatedAt, &authorID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Comment{}, 0, ErrCommentNotFound
	}
	return c, authorID, err
}

func (r *Repository) DeleteComment(ctx context.Context, itemID, id int64) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM library_comments WHERE item_id = $1 AND id = $2`, itemID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrCommentNotFound
	}
	return nil
}

// ----- resources (the cover and the other files) -----

const resourceSelect = `
	SELECT f.id, f.item_id, f.is_cover, f.title, f.file_name, f.content_type, f.file_size, u.login, f.created_at
	FROM library_resources f JOIN users u ON u.id = f.uploaded_by`

func scanResource(row pgx.Row, f *Resource) error {
	return row.Scan(&f.ID, &f.ItemID, &f.IsCover, &f.Title, &f.FileName, &f.ContentType, &f.FileSize,
		&f.UploadedBy, &f.CreatedAt)
}

// ListResources is the files of an item that are not its cover, oldest first.
func (r *Repository) ListResources(ctx context.Context, itemID int64) ([]Resource, error) {
	rows, err := r.pool.Query(ctx, resourceSelect+` WHERE f.item_id = $1 AND NOT f.is_cover ORDER BY f.id`, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []Resource{}
	for rows.Next() {
		var f Resource
		if err := scanResource(rows, &f); err != nil {
			return nil, err
		}
		list = append(list, f)
	}
	return list, rows.Err()
}

// GetResource is any file of the item, the cover included, and the id of whoever attached it.
func (r *Repository) GetResource(ctx context.Context, itemID, id int64) (Resource, int64, error) {
	var f Resource
	var uploaderID int64
	err := r.pool.QueryRow(ctx, `
		SELECT f.id, f.item_id, f.is_cover, f.title, f.file_name, f.content_type, f.file_size, u.login, f.created_at,
			f.uploaded_by
		FROM library_resources f JOIN users u ON u.id = f.uploaded_by
		WHERE f.item_id = $1 AND f.id = $2
	`, itemID, id).Scan(&f.ID, &f.ItemID, &f.IsCover, &f.Title, &f.FileName, &f.ContentType, &f.FileSize,
		&f.UploadedBy, &f.CreatedAt, &uploaderID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Resource{}, 0, ErrResourceNotFound
	}
	return f, uploaderID, err
}

// GetCover is the cover of the item.
func (r *Repository) GetCover(ctx context.Context, itemID int64) (Resource, error) {
	var f Resource
	err := scanResource(r.pool.QueryRow(ctx, resourceSelect+` WHERE f.item_id = $1 AND f.is_cover`, itemID), &f)
	if errors.Is(err, pgx.ErrNoRows) {
		return Resource{}, ErrCoverNotFound
	}
	return f, err
}

// CreateResource adds the row of a file. A cover takes the place of the one the item had: the old
// row is deleted in the same transaction and its id is the second answer, so that the caller can
// remove its bytes (0 when there was none).
func (r *Repository) CreateResource(ctx context.Context, itemID, uploadedBy int64, isCover bool, title *string,
	name, contentType string, size int) (Resource, int64, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Resource{}, 0, err
	}
	defer tx.Rollback(ctx)
	var replaced int64
	if isCover {
		err := tx.QueryRow(ctx, `DELETE FROM library_resources WHERE item_id = $1 AND is_cover RETURNING id`, itemID).Scan(&replaced)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return Resource{}, 0, err
		}
	}
	var id int64
	err = tx.QueryRow(ctx, `
		INSERT INTO library_resources (item_id, is_cover, title, file_name, content_type, file_size, uploaded_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id
	`, itemID, isCover, title, name, contentType, size, uploadedBy).Scan(&id)
	if err != nil {
		return Resource{}, 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Resource{}, 0, err
	}
	var f Resource
	err = scanResource(r.pool.QueryRow(ctx, resourceSelect+` WHERE f.id = $1`, id), &f)
	return f, replaced, err
}

func (r *Repository) DeleteResource(ctx context.Context, itemID, id int64) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM library_resources WHERE item_id = $1 AND id = $2`, itemID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrResourceNotFound
	}
	return nil
}

// ----- loans -----

const loanSelect = `
	SELECT l.id, l.item_id, b.title, u.login, l.lent_at::text, l.returned_at::text, rb.login, l.notes, l.created_at
	FROM library_loans l
	JOIN library_items b ON b.id = l.item_id
	JOIN users u ON u.id = l.user_id
	JOIN users rb ON rb.id = l.recorded_by`

func scanLoan(row pgx.Row, l *Loan) error {
	return row.Scan(&l.ID, &l.ItemID, &l.ItemTitle, &l.UserLogin, &l.LentAt, &l.ReturnedAt, &l.RecordedByLogin,
		&l.Notes, &l.CreatedAt)
}

func (r *Repository) queryLoans(ctx context.Context, where string, args ...any) ([]Loan, error) {
	rows, err := r.pool.Query(ctx, loanSelect+where+` ORDER BY l.lent_at DESC, l.id DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []Loan{}
	for rows.Next() {
		var l Loan
		if err := scanLoan(rows, &l); err != nil {
			return nil, err
		}
		list = append(list, l)
	}
	return list, rows.Err()
}

// ListLoansOfItem is the history of an item, the newest first.
func (r *Repository) ListLoansOfItem(ctx context.Context, itemID int64) ([]Loan, error) {
	return r.queryLoans(ctx, ` WHERE l.item_id = $1`, itemID)
}

// ListLoans is the loans of every item: only those still out, or all of them.
func (r *Repository) ListLoans(ctx context.Context, openOnly bool) ([]Loan, error) {
	if openOnly {
		return r.queryLoans(ctx, ` WHERE l.returned_at IS NULL`)
	}
	return r.queryLoans(ctx, ``)
}

func (r *Repository) GetLoan(ctx context.Context, itemID, id int64) (Loan, error) {
	var l Loan
	err := scanLoan(r.pool.QueryRow(ctx, loanSelect+` WHERE l.item_id = $1 AND l.id = $2`, itemID, id), &l)
	if errors.Is(err, pgx.ErrNoRows) {
		return Loan{}, ErrLoanNotFound
	}
	return l, err
}

// CreateLoan lends an item. The item's row is locked while it decides, and the unique index of open
// loans is the last word: an item that is out is ErrNotAvailable.
func (r *Repository) CreateLoan(ctx context.Context, itemID, userID, recordedBy int64, lentAt string, notes *string) (Loan, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Loan{}, err
	}
	defer tx.Rollback(ctx)
	var ended, beforeAcquired bool
	err = tx.QueryRow(ctx, `
		SELECT ended_on IS NOT NULL, $2::date < acquired_on FROM library_items WHERE id = $1 FOR UPDATE
	`, itemID, lentAt).Scan(&ended, &beforeAcquired)
	if errors.Is(err, pgx.ErrNoRows) {
		return Loan{}, ErrNotFound
	}
	if err != nil {
		return Loan{}, err
	}
	if ended {
		return Loan{}, ErrItemEnded
	}
	if beforeAcquired {
		return Loan{}, ErrInvalidDates
	}
	var id int64
	err = tx.QueryRow(ctx, `
		INSERT INTO library_loans (item_id, user_id, lent_at, recorded_by, notes)
		VALUES ($1, $2, $3::date, $4, $5) RETURNING id
	`, itemID, userID, lentAt, recordedBy, notes).Scan(&id)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolationCode {
		return Loan{}, ErrNotAvailable
	}
	if err != nil {
		return Loan{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Loan{}, err
	}
	var l Loan
	err = scanLoan(r.pool.QueryRow(ctx, loanSelect+` WHERE l.id = $1`, id), &l)
	return l, err
}

// ReturnLoan closes an open loan on the given day (not before it began: ErrInvalidDates).
func (r *Repository) ReturnLoan(ctx context.Context, itemID, id int64, returnedAt string) (Loan, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Loan{}, err
	}
	defer tx.Rollback(ctx)
	var open bool
	var beforeLent bool
	err = tx.QueryRow(ctx, `
		SELECT returned_at IS NULL, $3::date < lent_at FROM library_loans WHERE item_id = $1 AND id = $2 FOR UPDATE
	`, itemID, id, returnedAt).Scan(&open, &beforeLent)
	if errors.Is(err, pgx.ErrNoRows) {
		return Loan{}, ErrLoanNotFound
	}
	if err != nil {
		return Loan{}, err
	}
	if !open {
		return Loan{}, ErrAlreadyReturned
	}
	if beforeLent {
		return Loan{}, ErrInvalidDates
	}
	if _, err := tx.Exec(ctx, `UPDATE library_loans SET returned_at = $2::date WHERE id = $1`, id, returnedAt); err != nil {
		return Loan{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Loan{}, err
	}
	return r.GetLoan(ctx, itemID, id)
}

// DeleteLoan removes a loan recorded by mistake.
func (r *Repository) DeleteLoan(ctx context.Context, itemID, id int64) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM library_loans WHERE item_id = $1 AND id = $2`, itemID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrLoanNotFound
	}
	return nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == uniqueViolationCode
}

func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == foreignKeyViolationCode
}

// ----- types -----

// ListTypes is every type with how many items have it, in the order they were added.
func (r *Repository) ListTypes(ctx context.Context) ([]Type, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT t.id, t.name, (SELECT count(*) FROM library_items i WHERE i.type_id = t.id)
		FROM library_types t ORDER BY t.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []Type{}
	for rows.Next() {
		var t Type
		if err := rows.Scan(&t.ID, &t.Name, &t.ItemCount); err != nil {
			return nil, err
		}
		list = append(list, t)
	}
	return list, rows.Err()
}

func (r *Repository) GetType(ctx context.Context, id int64) (Type, error) {
	var t Type
	err := r.pool.QueryRow(ctx, `
		SELECT t.id, t.name, (SELECT count(*) FROM library_items i WHERE i.type_id = t.id)
		FROM library_types t WHERE t.id = $1`, id).Scan(&t.ID, &t.Name, &t.ItemCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return Type{}, ErrTypeNotFound
	}
	return t, err
}

func (r *Repository) CreateType(ctx context.Context, name string) (Type, error) {
	var id int64
	err := r.pool.QueryRow(ctx, `INSERT INTO library_types (name) VALUES ($1) RETURNING id`, name).Scan(&id)
	if isUniqueViolation(err) {
		return Type{}, ErrDuplicateType
	}
	if err != nil {
		return Type{}, err
	}
	return r.GetType(ctx, id)
}

func (r *Repository) RenameType(ctx context.Context, id int64, name string) (Type, error) {
	tag, err := r.pool.Exec(ctx, `UPDATE library_types SET name = $2 WHERE id = $1`, id, name)
	if isUniqueViolation(err) {
		return Type{}, ErrDuplicateType
	}
	if err != nil {
		return Type{}, err
	}
	if tag.RowsAffected() == 0 {
		return Type{}, ErrTypeNotFound
	}
	return r.GetType(ctx, id)
}

// DeleteType removes a type no item has: the foreign key refuses otherwise.
func (r *Repository) DeleteType(ctx context.Context, id int64) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM library_types WHERE id = $1`, id)
	if isForeignKeyViolation(err) {
		return ErrTypeInUse
	}
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrTypeNotFound
	}
	return nil
}

// TypeNamed is the type with that name, ignoring case, or ErrTypeNotFound.
func (r *Repository) TypeNamed(ctx context.Context, name string) (Type, error) {
	var id int64
	err := r.pool.QueryRow(ctx, `SELECT id FROM library_types WHERE lower(name) = lower($1)`, name).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Type{}, ErrTypeNotFound
	}
	if err != nil {
		return Type{}, err
	}
	return r.GetType(ctx, id)
}
