package inventory

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"katiuskas-intranet/backend/internal/platform/db"
)

var (
	ErrNotFound          = errors.New("item or transaction not found")
	ErrInsufficientStock = errors.New("not enough available quantity")
	ErrCannotReturnSale  = errors.New("sale transactions cannot be returned")
	ErrActivityRequired  = errors.New("loan and rental transactions must be linked to an activity")
)

// saleHasNoReturnConstraint is the CHECK constraint (see migration 00006)
// that keeps a sale from ever getting a returned_at date. ReturnTransaction
// relies on it as the single source of truth for "is this returnable"
// instead of duplicating the type check in Go.
const saleHasNoReturnConstraint = "sale_has_no_return"

// loanAndRentalRequireActivityConstraint is the CHECK constraint (see
// migration 00006) requiring loan/rental transactions to name the
// activity they're for.
const loanAndRentalRequireActivityConstraint = "loan_and_rental_require_activity"

type Repository struct {
	pool db.PGX
}

func NewRepository(pool db.PGX) *Repository {
	return &Repository{pool: pool}
}

func (r *Repository) CreateItem(ctx context.Context, in CreateItemInput) (Item, error) {
	var it Item
	err := r.pool.QueryRow(ctx, `
		INSERT INTO inventory_items (name, description, category, code, total_quantity,
			available_quantity, rental_price_per_week, sale_price)
		VALUES ($1,$2,$3,$4,$5,$5,$6,$7)
		RETURNING id, name, description, category, code, total_quantity, available_quantity,
			status, rental_price_per_week, sale_price
	`, in.Name, in.Description, in.Category, in.Code, in.TotalQuantity,
		in.RentalPricePerWeek, in.SalePrice,
	).Scan(&it.ID, &it.Name, &it.Description, &it.Category, &it.Code, &it.TotalQuantity,
		&it.AvailableQuantity, &it.Status, &it.RentalPricePerWeek, &it.SalePrice)
	return it, err
}

func (r *Repository) ListItems(ctx context.Context) ([]Item, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, name, description, category, code, total_quantity, available_quantity,
			status, rental_price_per_week, sale_price
		FROM inventory_items ORDER BY name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []Item{}
	for rows.Next() {
		var it Item
		if err := rows.Scan(&it.ID, &it.Name, &it.Description, &it.Category, &it.Code,
			&it.TotalQuantity, &it.AvailableQuantity, &it.Status, &it.RentalPricePerWeek,
			&it.SalePrice); err != nil {
			return nil, err
		}
		list = append(list, it)
	}
	return list, rows.Err()
}

// CreateTransaction decrements the item's available quantity and inserts
// the transaction row inside a single database transaction, so stock and
// history can never drift apart.
func (r *Repository) CreateTransaction(ctx context.Context, in CreateTransactionInput) (Transaction, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Transaction{}, err
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx, `
		UPDATE inventory_items SET available_quantity = available_quantity - $1
		WHERE id = $2 AND available_quantity >= $1
	`, in.Quantity, in.ItemID)
	if err != nil {
		return Transaction{}, err
	}
	if tag.RowsAffected() == 0 {
		return Transaction{}, ErrInsufficientStock
	}

	if in.Type == TransactionSale {
		if _, err := tx.Exec(ctx, `
			UPDATE inventory_items SET status = 'sold' WHERE id = $1 AND available_quantity = 0
		`, in.ItemID); err != nil {
			return Transaction{}, err
		}
	}

	var t Transaction
	err = tx.QueryRow(ctx, `
		INSERT INTO item_transactions (item_id, user_id, type, quantity, due_date, price, recorded_by, activity_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		RETURNING id, item_id, user_id, type, quantity, transacted_at, due_date, returned_at,
			price, status, recorded_by, activity_id
	`, in.ItemID, in.UserID, in.Type, in.Quantity, in.DueDate, in.Price, in.RecordedBy, in.ActivityID,
	).Scan(&t.ID, &t.ItemID, &t.UserID, &t.Type, &t.Quantity, &t.TransactedAt, &t.DueDate,
		&t.ReturnedAt, &t.Price, &t.Status, &t.RecordedBy, &t.ActivityID)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.ConstraintName == loanAndRentalRequireActivityConstraint {
		return Transaction{}, ErrActivityRequired
	}
	if err != nil {
		return Transaction{}, err
	}

	return t, tx.Commit(ctx)
}

// ReturnTransaction closes an active loan or rental and restores the
// item's available quantity, again inside a single transaction.
func (r *Repository) ReturnTransaction(ctx context.Context, id int64) (Transaction, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Transaction{}, err
	}
	defer tx.Rollback(ctx)

	var t Transaction
	err = tx.QueryRow(ctx, `
		UPDATE item_transactions SET returned_at = now(), status = 'completed'
		WHERE id = $1 AND status = 'active'
		RETURNING id, item_id, user_id, type, quantity, transacted_at, due_date, returned_at,
			price, status, recorded_by, activity_id
	`, id).Scan(&t.ID, &t.ItemID, &t.UserID, &t.Type, &t.Quantity, &t.TransactedAt, &t.DueDate,
		&t.ReturnedAt, &t.Price, &t.Status, &t.RecordedBy, &t.ActivityID)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.ConstraintName == saleHasNoReturnConstraint {
		return t, ErrCannotReturnSale
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return t, ErrNotFound
	}
	if err != nil {
		return t, err
	}

	if _, err := tx.Exec(ctx, `
		UPDATE inventory_items SET available_quantity = available_quantity + $1,
			status = 'available' WHERE id = $2
	`, t.Quantity, t.ItemID); err != nil {
		return t, err
	}

	return t, tx.Commit(ctx)
}

func (r *Repository) ListTransactionsByItem(ctx context.Context, itemID int64) ([]Transaction, error) {
	return r.queryTransactions(ctx, `WHERE item_id = $1 ORDER BY transacted_at DESC`, itemID)
}

func (r *Repository) ListTransactionsByStatus(ctx context.Context, status TransactionStatus) ([]Transaction, error) {
	return r.queryTransactions(ctx, `WHERE status = $1 ORDER BY transacted_at DESC`, status)
}

// ListTransactionsByUser returns every purchase, rental and loan a person
// has ever made, regardless of status — the material history shown on
// that person's page.
func (r *Repository) ListTransactionsByUser(ctx context.Context, userID int64) ([]Transaction, error) {
	return r.queryTransactions(ctx, `WHERE user_id = $1 ORDER BY transacted_at DESC`, userID)
}

func (r *Repository) queryTransactions(ctx context.Context, whereClause string, arg any) ([]Transaction, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, item_id, user_id, type, quantity, transacted_at, due_date, returned_at,
			price, status, recorded_by, activity_id
		FROM item_transactions `+whereClause, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []Transaction{}
	for rows.Next() {
		var t Transaction
		if err := rows.Scan(&t.ID, &t.ItemID, &t.UserID, &t.Type, &t.Quantity, &t.TransactedAt,
			&t.DueDate, &t.ReturnedAt, &t.Price, &t.Status, &t.RecordedBy, &t.ActivityID); err != nil {
			return nil, err
		}
		list = append(list, t)
	}
	return list, rows.Err()
}
