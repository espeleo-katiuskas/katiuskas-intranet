package material

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	// ErrAccountRequired means a sale or a rental that costs something was recorded with no account
	// to say where the money was paid into.
	ErrAccountRequired = errors.New("the account the money was paid into is required")
	// ErrUnknownAccount means that account does not exist.
	ErrUnknownAccount = errors.New("account not found")
	// ErrAccountNotAsset means the account the money was paid into is not of type asset.
	ErrAccountNotAsset = errors.New("the account the money was paid into must be of type asset")
	// ErrAccountHasChildren means one of the accounts involved groups others, so it cannot have
	// entries of its own.
	ErrAccountHasChildren = errors.New("an account that groups others cannot have transactions")
	// ErrNoMaterialAccount means no account is marked for the rentals (or the sales) of material,
	// so there is nowhere to record the income.
	ErrNoMaterialAccount = errors.New("no account is marked for the rentals or sales of material")
	// ErrMovementTransactionHasFiles means the movement cannot be deleted because the transaction it
	// recorded has files attached: they have to be removed first.
	ErrMovementTransactionHasFiles = errors.New("the transaction of this movement has files attached")
)

// accountHasChildrenConstraint is the trigger's error when an entry is posted to an account that
// has children.
const accountHasChildrenConstraint = "account_has_children"

// movementAmount is what a sale or a rental costs: its price (per unit; per unit and week for a
// rental, of which the first week is charged) times the quantity, to the cent.
func movementAmount(m Movement) float64 {
	if m.Price == nil {
		return 0
	}
	return math.Round(*m.Price*float64(m.Quantity)*100) / 100
}

// recordMovementTransaction records in the accounting a sale or a rental that was just inserted:
// the money goes into the asset account it was paid in (assetID) and out of the income account
// marked for rentals or for sales, in one transaction with a description in Spanish, and the
// movement points at it (m.TransactionID). Nothing is recorded for what costs nothing, which needs
// no account; money with no account said is ErrAccountRequired.
func (r *Repository) recordMovementTransaction(ctx context.Context, tx pgx.Tx, m *Movement, assetID *int64) error {
	amount := movementAmount(*m)
	if amount == 0 {
		return nil
	}
	if assetID == nil {
		return ErrAccountRequired
	}

	var kind *string
	err := tx.QueryRow(ctx, `SELECT account_effective_type(id)::text FROM accounts WHERE id = $1`, *assetID).Scan(&kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrUnknownAccount
	}
	if err != nil {
		return err
	}
	if kind == nil || *kind != "asset" {
		return ErrAccountNotAsset
	}

	marked := "material_sale"
	if m.Type == MovementRental {
		marked = "material_rental"
	}
	var incomeID int64
	err = tx.QueryRow(ctx, `SELECT id FROM accounts WHERE `+marked).Scan(&incomeID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNoMaterialAccount
	}
	if err != nil {
		return err
	}

	description, err := r.movementDescription(ctx, tx, *m)
	if err != nil {
		return err
	}
	var transactionID int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO transactions (description, occurred_at) VALUES ($1, $2) RETURNING id
	`, description, m.TransactedAt).Scan(&transactionID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO transaction_entries (transaction_id, account_id, amount) VALUES ($1, $2, $4), ($1, $3, -$4::numeric)
	`, transactionID, *assetID, incomeID, amount); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.ConstraintName == accountHasChildrenConstraint {
			return ErrAccountHasChildren
		}
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE material_movements SET transaction_id = $2 WHERE id = $1`, m.ID, transactionID); err != nil {
		return err
	}
	m.TransactionID = &transactionID
	return nil
}

// movementDescription says in Spanish what a sale or a rental was: what, how many and to whom.
func (r *Repository) movementDescription(ctx context.Context, tx pgx.Tx, m Movement) (string, error) {
	item, err := r.GetItem(ctx, m.ItemID)
	if err != nil {
		return "", err
	}
	description := fmt.Sprintf("Venta de %d × %s", m.Quantity, item.DisplayName)
	if m.Type == MovementRental {
		description = fmt.Sprintf("Alquiler de %d × %s", m.Quantity, item.DisplayName)
	}
	if m.UserID != nil {
		var name string
		if err := tx.QueryRow(ctx, `SELECT first_name || ' ' || last_name FROM users WHERE id = $1`, *m.UserID).Scan(&name); err != nil {
			return "", err
		}
		description += " a " + name
	}
	return description, nil
}

// deleteMovementTransaction removes the transaction a deleted movement had recorded, with its
// entries (nothing cascades). The movement is already gone, so nothing points at it any more; a
// transaction with files attached is refused (ErrMovementTransactionHasFiles).
func deleteMovementTransaction(ctx context.Context, tx pgx.Tx, transactionID int64) error {
	if _, err := tx.Exec(ctx, `DELETE FROM transaction_entries WHERE transaction_id = $1`, transactionID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `DELETE FROM transactions WHERE id = $1`, transactionID)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == foreignKeyViolationCode && pgErr.TableName == "transaction_files" {
		return ErrMovementTransactionHasFiles
	}
	return err
}
