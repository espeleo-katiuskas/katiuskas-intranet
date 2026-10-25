package distributions

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/platform/db"
)

// ErrPaymentsNotUndoable means the payments a transaction recorded can't be taken
// back automatically because the orders have changed since: what a payment paid
// is not paid any more (or a payment taken off can't be paid again).
var ErrPaymentsNotUndoable = errors.New("the payments of this transaction can't be undone: the orders they were about have changed since")

const checkViolationCode = "23514"

// UndoPaymentsOf makes it as if the payments (and payments taken off) a
// transaction recorded had never been made: what each paid is unpaid again, what
// each took off is paid again, and their entries leave the list of payments. It
// runs on the transaction of whoever is deleting the accounting transaction, so
// both go or neither does, and it's what lets a payment made by mistake be
// undone by deleting its transaction. Entries of orders that no longer exist
// have nothing to undo. ErrPaymentsNotUndoable when an order no longer allows it.
func UndoPaymentsOf(ctx context.Context, tx db.PGX, transactionID int64) error {
	type entry struct {
		unitID      *int64
		quantity    int
		identifiers []string
		recordedBy  *int64
	}
	rows, err := tx.Query(ctx, `
		SELECT unit_id, quantity, identifiers, recorded_by_id
		FROM distribution_payments WHERE transaction_id = $1 ORDER BY id DESC
	`, transactionID)
	if err != nil {
		return err
	}
	var entries []entry
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.unitID, &e.quantity, &e.identifiers, &e.recordedBy); err != nil {
			rows.Close()
			return err
		}
		entries = append(entries, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, e := range entries {
		if e.unitID == nil {
			continue
		}
		var changed, items int64
		if e.quantity > 0 {
			tag, err := tx.Exec(ctx, `
				UPDATE distribution_units SET paid_quantity = paid_quantity - $2,
					paid_at = CASE WHEN paid_quantity - $2 = 0 THEN NULL ELSE paid_at END,
					paid_marked_by_id = CASE WHEN paid_quantity - $2 = 0 THEN NULL ELSE paid_marked_by_id END
				WHERE id = $1 AND paid_quantity >= $2
			`, *e.unitID, e.quantity)
			if err := undoError(err); err != nil {
				return err
			}
			changed = tag.RowsAffected()
			if len(e.identifiers) > 0 {
				tag, err := tx.Exec(ctx, `
					UPDATE distribution_unit_identifiers SET paid_at = NULL, paid_marked_by_id = NULL
					WHERE unit_id = $1 AND identifier = ANY($2) AND paid_at IS NOT NULL
				`, *e.unitID, e.identifiers)
				if err := undoError(err); err != nil {
					return err
				}
				items = tag.RowsAffected()
			}
		} else {
			tag, err := tx.Exec(ctx, `
				UPDATE distribution_units SET paid_quantity = paid_quantity + $2, paid_at = now(), paid_marked_by_id = $3
				WHERE id = $1 AND paid_quantity + $2 <= quantity
			`, *e.unitID, -e.quantity, e.recordedBy)
			if err := undoError(err); err != nil {
				return err
			}
			changed = tag.RowsAffected()
			if len(e.identifiers) > 0 {
				tag, err := tx.Exec(ctx, `
					UPDATE distribution_unit_identifiers SET paid_at = now(), paid_marked_by_id = $3
					WHERE unit_id = $1 AND identifier = ANY($2) AND paid_at IS NULL
				`, *e.unitID, e.identifiers, e.recordedBy)
				if err := undoError(err); err != nil {
					return err
				}
				items = tag.RowsAffected()
			}
		}
		if changed != 1 || items != int64(len(e.identifiers)) {
			return ErrPaymentsNotUndoable
		}
	}
	_, err = tx.Exec(ctx, `DELETE FROM distribution_payments WHERE transaction_id = $1`, transactionID)
	return err
}

// undoError turns a constraint of the orders refusing a change into
// ErrPaymentsNotUndoable.
func undoError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == checkViolationCode {
		return ErrPaymentsNotUndoable
	}
	return err
}
