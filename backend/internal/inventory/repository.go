package inventory

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/platform/db"
)

var (
	ErrNotFound          = errors.New("item or transaction not found")
	ErrInsufficientStock = errors.New("not enough available quantity")
	ErrCannotReturnSale  = errors.New("sale transactions cannot be returned")
	ErrActivityRequired  = errors.New("loan and rental transactions must be linked to an activity")
	// ErrNotLoanable means the item's loanable flag is false — see
	// Item.Loanable.
	ErrNotLoanable = errors.New("this item cannot be loaned")
	// ErrCategoryNotFound means the given category_id isn't in the
	// material_categories catalog.
	ErrCategoryNotFound = errors.New("material category not found")
)

// categoryFKViolationCode is the foreign-key violation Postgres raises
// when CreateItem is given a category_id absent from material_categories.
const categoryFKViolationCode = "23503"

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

// itemSelect/scanItem are shared by every query that returns one or more
// Item rows other than CreateItem's INSERT ... RETURNING (which can't
// JOIN against the row it just wrote — see CreateItem's own follow-up
// query for the category name).
const itemSelect = `
	i.id, i.item_type, i.brand, i.model, i.code, i.description, i.acquired_at,
	i.total_quantity, i.available_quantity, i.status, i.loanable,
	i.rental_price_per_week, i.sale_price,
	i.rope_type, i.diameter_mm, i.manufacture_year, i.bag_type, i.capacity_liters,
	i.category_id, mc.name
	FROM inventory_items i
	LEFT JOIN material_categories mc ON mc.id = i.category_id`

func scanItem(row interface{ Scan(dest ...any) error }, it *Item) error {
	if err := row.Scan(&it.ID, &it.Type, &it.Brand, &it.Model, &it.Code, &it.Description, &it.AcquiredAt,
		&it.TotalQuantity, &it.AvailableQuantity, &it.Status, &it.Loanable,
		&it.RentalPricePerWeek, &it.SalePrice,
		&it.RopeType, &it.DiameterMM, &it.ManufactureYear, &it.BagType, &it.CapacityLiters,
		&it.CategoryID, &it.CategoryName,
	); err != nil {
		return err
	}
	it.Rentable = it.RentalPricePerWeek != nil
	it.Sellable = it.SalePrice != nil
	return nil
}

func (r *Repository) CreateItem(ctx context.Context, in CreateItemInput) (Item, error) {
	var it Item
	err := r.pool.QueryRow(ctx, `
		INSERT INTO inventory_items (item_type, brand, model, code, description, acquired_at,
			total_quantity, available_quantity, loanable, rental_price_per_week, sale_price,
			rope_type, diameter_mm, manufacture_year, bag_type, capacity_liters, category_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
		RETURNING id, item_type, brand, model, code, description, acquired_at,
			total_quantity, available_quantity, status, loanable, rental_price_per_week, sale_price,
			rope_type, diameter_mm, manufacture_year, bag_type, capacity_liters, category_id
	`, in.Type, in.Brand, in.Model, in.Code, in.Description, in.AcquiredAt,
		in.TotalQuantity, in.Loanable, in.RentalPricePerWeek, in.SalePrice,
		in.RopeType, in.DiameterMM, in.ManufactureYear, in.BagType, in.CapacityLiters, in.CategoryID,
	).Scan(&it.ID, &it.Type, &it.Brand, &it.Model, &it.Code, &it.Description, &it.AcquiredAt,
		&it.TotalQuantity, &it.AvailableQuantity, &it.Status, &it.Loanable, &it.RentalPricePerWeek, &it.SalePrice,
		&it.RopeType, &it.DiameterMM, &it.ManufactureYear, &it.BagType, &it.CapacityLiters, &it.CategoryID)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == categoryFKViolationCode {
		return Item{}, ErrCategoryNotFound
	}
	if err != nil {
		return it, err
	}
	if it.CategoryID != nil {
		var name string
		if err := r.pool.QueryRow(ctx, `SELECT name FROM material_categories WHERE id = $1`, *it.CategoryID).Scan(&name); err != nil {
			return it, err
		}
		it.CategoryName = &name
	}
	it.Rentable = it.RentalPricePerWeek != nil
	it.Sellable = it.SalePrice != nil
	return it, nil
}

func (r *Repository) ListItems(ctx context.Context) ([]Item, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+itemSelect+`
		ORDER BY i.item_type, i.brand, i.model, i.id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []Item{}
	for rows.Next() {
		var it Item
		if err := scanItem(rows, &it); err != nil {
			return nil, err
		}
		list = append(list, it)
	}
	return list, rows.Err()
}

// ListMaterialCategories returns the gear category catalog (migration
// 00013) — every entry, in name order, for the "new item" form's
// category picker.
func (r *Repository) ListMaterialCategories(ctx context.Context) ([]MaterialCategory, error) {
	rows, err := r.pool.Query(ctx, `SELECT id, name FROM material_categories ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []MaterialCategory{}
	for rows.Next() {
		var c MaterialCategory
		if err := rows.Scan(&c.ID, &c.Name); err != nil {
			return nil, err
		}
		list = append(list, c)
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

	if in.Type == TransactionLoan {
		var loanable bool
		err := tx.QueryRow(ctx, `SELECT loanable FROM inventory_items WHERE id = $1`, in.ItemID).Scan(&loanable)
		if errors.Is(err, pgx.ErrNoRows) {
			return Transaction{}, ErrInsufficientStock
		}
		if err != nil {
			return Transaction{}, err
		}
		if !loanable {
			return Transaction{}, ErrNotLoanable
		}
	}

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
	return r.queryTransactions(ctx, `WHERE it.item_id = $1 ORDER BY it.transacted_at DESC`, itemID)
}

func (r *Repository) ListTransactionsByStatus(ctx context.Context, status TransactionStatus) ([]Transaction, error) {
	return r.queryTransactions(ctx, `WHERE it.status = $1 ORDER BY it.transacted_at DESC`, status)
}

// ListTransactionsByUser returns every purchase, rental and loan a person
// has ever made, regardless of status — the material history shown on
// that person's page.
func (r *Repository) ListTransactionsByUser(ctx context.Context, userID int64) ([]Transaction, error) {
	return r.queryTransactions(ctx, `WHERE it.user_id = $1 ORDER BY it.transacted_at DESC`, userID)
}

// queryTransactions joins in the login of both the transaction's party
// and, if recorded, whoever logged it — every caller here can span many
// different people at once (by item, by status, or even by user, since
// nothing stops the same listing shape from being reused later), so a
// JOIN is simpler than resolving each id after the fact the way the two
// single-row RETURNING writes below have to.
func (r *Repository) queryTransactions(ctx context.Context, whereClause string, arg any) ([]Transaction, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT it.id, it.item_id, it.user_id, u.login, it.type, it.quantity, it.transacted_at, it.due_date,
			it.returned_at, it.price, it.status, it.recorded_by, ru.login, it.activity_id
		FROM item_transactions it
		JOIN users u ON u.id = it.user_id
		LEFT JOIN users ru ON ru.id = it.recorded_by
		`+whereClause, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []Transaction{}
	for rows.Next() {
		var t Transaction
		if err := rows.Scan(&t.ID, &t.ItemID, &t.UserID, &t.UserLogin, &t.Type, &t.Quantity, &t.TransactedAt,
			&t.DueDate, &t.ReturnedAt, &t.Price, &t.Status, &t.RecordedBy, &t.RecordedByLogin, &t.ActivityID); err != nil {
			return nil, err
		}
		list = append(list, t)
	}
	return list, rows.Err()
}
