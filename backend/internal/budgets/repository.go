package budgets

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

// ledgerAccount is an account of the ledger with what the budget rules need to know about it.
type ledgerAccount struct {
	AccountRef
	EffectiveType string // "", "asset", "income" or "expense"
	IsGroup       bool
}

func (r *Repository) ledger(ctx context.Context) (map[int64]ledgerAccount, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT a.id, a.name, COALESCE(a.code, ''), a.hidden,
		       COALESCE(account_effective_type(a.id)::text, ''),
		       EXISTS (SELECT 1 FROM accounts c WHERE c.parent_id = a.id)
		FROM accounts a`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]ledgerAccount{}
	for rows.Next() {
		var a ledgerAccount
		if err := rows.Scan(&a.ID, &a.Name, &a.Code, &a.Hidden, &a.EffectiveType, &a.IsGroup); err != nil {
			return nil, err
		}
		out[a.ID] = a
	}
	return out, rows.Err()
}

// netByAccount is, per account, the sum of the entries posted in the calendar year (positive:
// money into the account).
func (r *Repository) netByAccount(ctx context.Context, year int) (map[int64]float64, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT e.account_id, SUM(e.amount)::float8
		FROM transaction_entries e JOIN transactions t ON t.id = e.transaction_id
		WHERE t.occurred_at >= make_timestamptz($1, 1, 1, 0, 0, 0, 'UTC')
		  AND t.occurred_at < make_timestamptz($1 + 1, 1, 1, 0, 0, 0, 'UTC')
		GROUP BY e.account_id`, year)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]float64{}
	for rows.Next() {
		var id int64
		var sum float64
		if err := rows.Scan(&id, &sum); err != nil {
			return nil, err
		}
		out[id] = sum
	}
	return out, rows.Err()
}

type storedItem struct {
	Kind       Kind
	Name       string
	Amount     float64
	AccountIDs []int64
}

// years lists the years that have a budget, newest first.
func (r *Repository) years(ctx context.Context) ([]int, error) {
	rows, err := r.pool.Query(ctx, `SELECT year FROM budgets ORDER BY year DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []int{}
	for rows.Next() {
		var y int
		if err := rows.Scan(&y); err != nil {
			return nil, err
		}
		out = append(out, y)
	}
	return out, rows.Err()
}

// stored returns the line items of a year in their order, or ErrNotFound if it has no budget.
func (r *Repository) stored(ctx context.Context, year int) ([]storedItem, error) {
	var budgetID int64
	if err := r.pool.QueryRow(ctx, `SELECT id FROM budgets WHERE year = $1`, year).Scan(&budgetID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	rows, err := r.pool.Query(ctx, `
		SELECT i.kind::text, i.name, i.amount::float8,
		       COALESCE(array_agg(ia.account_id ORDER BY ia.account_id) FILTER (WHERE ia.account_id IS NOT NULL), '{}')
		FROM budget_items i LEFT JOIN budget_item_accounts ia ON ia.item_id = i.id
		WHERE i.budget_id = $1
		GROUP BY i.id
		ORDER BY i.kind, i.position, i.id`, budgetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []storedItem{}
	for rows.Next() {
		var it storedItem
		var kind string
		if err := rows.Scan(&kind, &it.Name, &it.Amount, &it.AccountIDs); err != nil {
			return nil, err
		}
		it.Kind = Kind(kind)
		out = append(out, it)
	}
	return out, rows.Err()
}

// replace saves the budget of a year, creating it or swapping its line items for the given ones.
func (r *Repository) replace(ctx context.Context, year int, in Input) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var budgetID int64
	err = tx.QueryRow(ctx, `
		INSERT INTO budgets (year) VALUES ($1)
		ON CONFLICT (year) DO UPDATE SET year = EXCLUDED.year
		RETURNING id`, year).Scan(&budgetID)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM budget_items WHERE budget_id = $1`, budgetID); err != nil {
		return err
	}
	for _, part := range []struct {
		kind  Kind
		items []ItemInput
	}{{KindIncome, in.Income}, {KindExpense, in.Expenses}} {
		for position, it := range part.items {
			var itemID int64
			err := tx.QueryRow(ctx, `
				INSERT INTO budget_items (budget_id, kind, name, amount, position)
				VALUES ($1, $2, $3, $4::numeric / 100, $5) RETURNING id`,
				budgetID, string(part.kind), it.Name, it.Cents, position+1).Scan(&itemID)
			if err != nil {
				return err
			}
			for _, accountID := range it.AccountIDs {
				if _, err := tx.Exec(ctx, `
					INSERT INTO budget_item_accounts (item_id, budget_id, account_id) VALUES ($1, $2, $3)`,
					itemID, budgetID, accountID); err != nil {
					return err
				}
			}
		}
	}
	return tx.Commit(ctx)
}

func (r *Repository) delete(ctx context.Context, year int) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM budgets WHERE year = $1`, year)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
