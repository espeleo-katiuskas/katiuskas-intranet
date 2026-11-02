package material

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/platform/db"
)

var (
	ErrNotFound          = errors.New("item or movement not found")
	ErrInsufficientStock = errors.New("not enough available quantity")
	ErrActivityRequired  = errors.New("loan and return movements must be linked to an activity")
	// ErrNotLoanable means the item's loanable flag is false — see
	// Item.Loanable.
	ErrNotLoanable = errors.New("this item cannot be loaned")
	// ErrNotRentable/ErrNotSellable mean the item has no
	// rental_price_per_week/sale_price on file — see Item.Rentable/
	// Item.Sellable, which are fully derived from that. Without this
	// check, CreateMovement would insert a 'rental'/'sale' movement with
	// a nil price anyway, hitting migration 00006_inventory's own
	// price_required_for_rental_and_sale CHECK constraint instead —
	// unmapped, so it would have surfaced as a raw 500.
	ErrNotRentable = errors.New("this item cannot be rented (no rental price set)")
	ErrNotSellable = errors.New("this item cannot be sold (no sale price set)")
	// ErrCategoryNotFound means the given category_id isn't in the
	// material_categories catalog.
	ErrCategoryNotFound = errors.New("material category not found")
	// ErrSingleUnitItem means either a rope or bag was asked to start
	// with (CreateItem) or gain (CreateMovement's 'acquisition') more
	// than its one single unit — those are always exactly one
	// individually-tracked object (rope_and_bag_require_code and their
	// own individual code already make a second unit meaningless; a
	// new one is a new item, not more stock on this one).
	ErrSingleUnitItem = errors.New("rope and bag items are always a single unit")
	// ErrReturnExceedsLoaned means a 'return' movement's quantity is
	// more than this item still has outstanding *for that activity* —
	// see Repository.CreateMovement.
	ErrReturnExceedsLoaned = errors.New("cannot return more than is currently on loan for this activity")
	// ErrInvalidItemFields means CreateItem's given fields don't fit
	// type's own required shape — a rope or bag with no
	// code/brand/model, a rope without its own rope-only fields (or a
	// non-rope with them), a bag without bag_type/capacity_liters (or a
	// non-bag with them), or a general item with no category_id/
	// description (see migration 00006_inventory.sql's own CHECK
	// constraints). There are too many distinct constraints here to
	// give each its own sentinel error the way ErrCategoryNotFound does
	// for the one foreign key on this same INSERT — every case instead
	// wraps this one sentinel with %w, so errors.Is still matches
	// regardless of which specific constraint fired, while the
	// message itself (built from Postgres's own ConstraintName) still
	// says which one.
	ErrInvalidItemFields = errors.New("item fields do not match type's required shape")
	// ErrRecipientNotMember means a loan or a rental was for somebody who is not a member now, and
	// ErrRecipientNotMemberOrFormer a sale for somebody who is neither a member nor a former one.
	ErrRecipientNotMember         = errors.New("a loan or a rental can only be for a member")
	ErrRecipientNotMemberOrFormer = errors.New("a sale can only be for a member or a former member")
	// ErrModelWithoutBrand means a model was given with no brand: a model is the model of a brand.
	ErrModelWithoutBrand = errors.New("a model can only be given when there is a brand")
	// ErrDuplicateItemCode means another item already has the code.
	ErrDuplicateItemCode = errors.New("another item already has this code")
)

// uniqueViolationCode is the unique violation Postgres raises for an item code that another item has.
const uniqueViolationCode = "23505"

// categoryFKViolationCode is the foreign-key violation Postgres raises
// when CreateItem is given a category_id absent from material_categories.
const categoryFKViolationCode = "23503"

// checkViolationCode is any CHECK constraint violation — see
// ErrInvalidItemFields, the only place this is checked for.
const checkViolationCode = "23514"

// activityRequiredConstraint is the CHECK constraint (see migration
// 00006_inventory.sql) requiring a loan or return to name the activity it's for.
// Rental's activity is optional — a rental doesn't always happen as
// part of one.
const activityRequiredConstraint = "activity_required_for_loan_and_return"

type Repository struct {
	pool db.PGX
}

func NewRepository(pool db.PGX) *Repository {
	return &Repository{pool: pool}
}

// itemSelect/scanItem are shared by every query that returns one or more
// Item rows other than CreateItem's INSERT ... RETURNING (which can't
// JOIN against the row it just wrote — see CreateItem's own follow-up
// queries for the category name and computed quantities).
// material_item_quantities computes total/available/outstanding-loan
// quantity from this item's own movement history, so they can never
// drift from what actually happened the way stored, manually-updated
// columns could.
const itemSelect = `
	i.id, i.type, i.brand, i.model, i.code, i.description,
	q.total_quantity, q.available_quantity, q.outstanding_loan_quantity, i.status, i.loanable,
	i.rental_price_per_week, i.sale_price,
	i.rope_type, i.diameter_mm, i.manufacture_year, i.length_m, i.bag_type, i.capacity_liters,
	i.category_id, mc.name
	FROM material_items i
	JOIN material_item_quantities q ON q.item_id = i.id
	LEFT JOIN material_categories mc ON mc.id = i.category_id`

func scanItem(row interface{ Scan(dest ...any) error }, it *Item) error {
	if err := row.Scan(&it.ID, &it.Type, &it.Brand, &it.Model, &it.Code, &it.Description,
		&it.TotalQuantity, &it.AvailableQuantity, &it.OutstandingLoanQuantity, &it.Status, &it.Loanable,
		&it.RentalPricePerWeek, &it.SalePrice,
		&it.RopeType, &it.DiameterMM, &it.ManufactureYear, &it.LengthM, &it.BagType, &it.CapacityLiters,
		&it.CategoryID, &it.CategoryName,
	); err != nil {
		return err
	}
	it.Rentable = it.RentalPricePerWeek != nil
	it.Sellable = it.SalePrice != nil
	return nil
}

// CreateItem inserts the item row and its first 'acquisition' movement
// (for in.InitialQuantity units) in a single database transaction, so
// an item can never exist without the movement history its computed
// quantities depend on.
func (r *Repository) CreateItem(ctx context.Context, in CreateItemInput) (Item, error) {
	initialQuantity := in.InitialQuantity
	if initialQuantity <= 0 {
		initialQuantity = 1
	}
	if (in.Type == ItemTypeRope || in.Type == ItemTypeBag) && initialQuantity != 1 {
		return Item{}, ErrSingleUnitItem
	}
	acquiredAt := time.Now()
	if in.AcquiredAt != nil {
		acquiredAt = *in.AcquiredAt
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Item{}, err
	}
	defer tx.Rollback(ctx)

	var it Item
	err = tx.QueryRow(ctx, `
		INSERT INTO material_items (type, brand, model, code, description, loanable,
			rental_price_per_week, sale_price, rope_type, diameter_mm, manufacture_year, length_m,
			bag_type, capacity_liters, category_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
		RETURNING id, type, brand, model, code, description, status, loanable,
			rental_price_per_week, sale_price, rope_type, diameter_mm, manufacture_year, length_m,
			bag_type, capacity_liters, category_id
	`, in.Type, in.Brand, in.Model, in.Code, in.Description, in.Loanable,
		in.RentalPricePerWeek, in.SalePrice, in.RopeType, in.DiameterMM, in.ManufactureYear, in.LengthM,
		in.BagType, in.CapacityLiters, in.CategoryID,
	).Scan(&it.ID, &it.Type, &it.Brand, &it.Model, &it.Code, &it.Description, &it.Status, &it.Loanable,
		&it.RentalPricePerWeek, &it.SalePrice, &it.RopeType, &it.DiameterMM, &it.ManufactureYear, &it.LengthM,
		&it.BagType, &it.CapacityLiters, &it.CategoryID)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case categoryFKViolationCode:
			return Item{}, ErrCategoryNotFound
		case checkViolationCode:
			return Item{}, fmt.Errorf("fields do not fit type %q (violates %s): %w",
				in.Type, pgErr.ConstraintName, ErrInvalidItemFields)
		case uniqueViolationCode:
			return Item{}, ErrDuplicateItemCode
		}
	}
	if err != nil {
		return Item{}, err
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO material_movements (item_id, type, quantity, transacted_at, status)
		VALUES ($1, 'acquisition', $2, $3, 'completed')
	`, it.ID, initialQuantity, acquiredAt); err != nil {
		return Item{}, err
	}

	if it.CategoryID != nil {
		var name string
		if err := tx.QueryRow(ctx, `SELECT name FROM material_categories WHERE id = $1`, *it.CategoryID).Scan(&name); err != nil {
			return Item{}, err
		}
		it.CategoryName = &name
	}
	it.Rentable = it.RentalPricePerWeek != nil
	it.Sellable = it.SalePrice != nil
	it.TotalQuantity = initialQuantity
	it.AvailableQuantity = initialQuantity

	return it, tx.Commit(ctx)
}

// UpdateItem replaces the editable fields of an item (see UpdateItemInput) and answers with it as
// GetItem would. The same constraints as when it was created apply, for its own type.
func (r *Repository) UpdateItem(ctx context.Context, id int64, in UpdateItemInput) (Item, error) {
	// In its own (sub)transaction so a rejected update is rolled back on its own: under test, where
	// every call shares one outer transaction, the violation would otherwise abort all of it.
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Item{}, err
	}
	defer tx.Rollback(ctx)

	var itemType ItemType
	err = tx.QueryRow(ctx, `
		UPDATE material_items SET brand=$2, model=$3, code=$4, description=$5, loanable=$6,
			rental_price_per_week=$7, sale_price=$8, rope_type=$9, diameter_mm=$10,
			manufacture_year=$11, length_m=$12, bag_type=$13, capacity_liters=$14, category_id=$15
		WHERE id = $1
		RETURNING type
	`, id, in.Brand, in.Model, in.Code, in.Description, in.Loanable,
		in.RentalPricePerWeek, in.SalePrice, in.RopeType, in.DiameterMM, in.ManufactureYear, in.LengthM,
		in.BagType, in.CapacityLiters, in.CategoryID,
	).Scan(&itemType)
	if errors.Is(err, pgx.ErrNoRows) {
		return Item{}, ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case categoryFKViolationCode:
			return Item{}, ErrCategoryNotFound
		case checkViolationCode:
			return Item{}, fmt.Errorf("fields do not fit type %q (violates %s): %w",
				itemType, pgErr.ConstraintName, ErrInvalidItemFields)
		case uniqueViolationCode:
			return Item{}, ErrDuplicateItemCode
		}
	}
	if err != nil {
		return Item{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Item{}, err
	}
	return r.GetItem(ctx, id)
}

// GetItem looks up a single item by id — used to name it in a mutation
// notification (see Handler.createMovement/returnMovement), since a
// Movement only carries an ItemID, not the item's own brand/model/code.
func (r *Repository) GetItem(ctx context.Context, id int64) (Item, error) {
	var it Item
	err := scanItem(r.pool.QueryRow(ctx, `
		SELECT `+itemSelect+`
		WHERE i.id = $1
	`, id), &it)
	if errors.Is(err, pgx.ErrNoRows) {
		return it, ErrNotFound
	}
	return it, err
}

func (r *Repository) ListItems(ctx context.Context) ([]Item, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+itemSelect+`
		ORDER BY i.type, i.brand, i.model, i.id
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

// ListMaterialCategories returns the material category catalog (migration
// 00006_inventory.sql) — every entry, in name order, for the "new item"
// form's category picker.
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

// itemStatusForMovement maps a permanent stock-removal movement type to
// the status an item gets once it has no units left — the same thing a
// sale always did, now shared with loss/abandonment/retirement.
var itemStatusForMovement = map[MovementType]ItemStatus{
	MovementSale:        ItemSold,
	MovementLoss:        ItemLost,
	MovementAbandonment: ItemAbandoned,
	MovementRetirement:  ItemRetired,
}

func isTerminalItemStatus(s ItemStatus) bool {
	switch s {
	case ItemSold, ItemLost, ItemAbandoned, ItemRetired:
		return true
	}
	return false
}

// affectsItemStatus reports whether a movement type can change an
// item's overall status: draining the last unit (sale/loss/abandonment/
// retirement) or restocking a depleted one (acquisition or a loan
// coming back via return). Rental is temporary and never touches it
// here — ReturnMovement resets status to available on its own once one
// comes back.
func affectsItemStatus(t MovementType) bool {
	_, terminal := itemStatusForMovement[t]
	return terminal || t == MovementAcquisition || t == MovementReturn
}

// syncItemStatusAfterMovement keeps Item.Status in step with a movement
// that changed the item's stock level for good: draining the last unit
// sets a terminal status (sold/lost/abandoned/retired), and restocking
// a depleted item via a further acquisition or return clears it back
// to available. Rental never reaches here — it's temporary, and
// ReturnMovement already resets status to available on its own.
func (r *Repository) syncItemStatusAfterMovement(ctx context.Context, tx pgx.Tx, itemID int64, movementType MovementType) error {
	var available int32
	var status ItemStatus
	if err := tx.QueryRow(ctx, `
		SELECT q.available_quantity, i.status FROM material_items i
		JOIN material_item_quantities q ON q.item_id = i.id
		WHERE i.id = $1
	`, itemID).Scan(&available, &status); err != nil {
		return err
	}

	if newStatus, ok := itemStatusForMovement[movementType]; ok && available == 0 {
		_, err := tx.Exec(ctx, `UPDATE material_items SET status = $1 WHERE id = $2`, newStatus, itemID)
		return err
	}
	if (movementType == MovementAcquisition || movementType == MovementReturn) && available > 0 && isTerminalItemStatus(status) {
		_, err := tx.Exec(ctx, `UPDATE material_items SET status = 'available' WHERE id = $1`, itemID)
		return err
	}
	return nil
}

// CreateMovement locks the item row for the duration of the check, so
// concurrent movements against the same item are serialized and can
// never oversell it, then inserts the movement row. Quantities are
// never stored on material_items — they're always recomputed from
// material_movements — so there's no stock column here left to update.
func (r *Repository) CreateMovement(ctx context.Context, in CreateMovementInput) (Movement, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Movement{}, err
	}
	defer tx.Rollback(ctx)

	var itemType ItemType
	var loanable bool
	var rentalPrice, salePrice *float64
	err = tx.QueryRow(ctx, `
		SELECT type, loanable, rental_price_per_week, sale_price
		FROM material_items WHERE id = $1 FOR UPDATE
	`, in.ItemID).Scan(&itemType, &loanable, &rentalPrice, &salePrice)
	if errors.Is(err, pgx.ErrNoRows) {
		return Movement{}, ErrNotFound
	}
	if err != nil {
		return Movement{}, err
	}

	// Rope and bag are individually tracked (always exactly one unit):
	// every movement type on them targets that single unit, and a
	// further acquisition makes no sense — a new rope is a new item.
	if itemType == ItemTypeRope || itemType == ItemTypeBag {
		if in.Type == MovementAcquisition {
			return Movement{}, ErrSingleUnitItem
		}
		in.Quantity = 1
	}

	// Price is never given by the caller — it's always the item's own
	// catalog price for that kind of movement, and always nil except
	// for rental/sale.
	var price *float64
	switch in.Type {
	case MovementLoan:
		if !loanable {
			return Movement{}, ErrNotLoanable
		}
	case MovementRental:
		if rentalPrice == nil {
			return Movement{}, ErrNotRentable
		}
		price = rentalPrice
	case MovementSale:
		if salePrice == nil {
			return Movement{}, ErrNotSellable
		}
		price = salePrice
	}

	switch in.Type {
	case MovementAcquisition:
		// No check — adds new stock the club now owns.
	case MovementReturn:
		// A return isn't linked to any one specific loan row — it's
		// validated against how much of this item that activity still
		// has outstanding overall (every loan minus every return, for
		// that item and activity together).
		if in.ActivityID == nil {
			return Movement{}, ErrActivityRequired
		}
		var outstandingForActivity int32
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(SUM(CASE WHEN type = 'loan' THEN quantity ELSE -quantity END), 0)
			FROM material_movements WHERE item_id = $1 AND activity_id = $2 AND type IN ('loan', 'return')
		`, in.ItemID, *in.ActivityID).Scan(&outstandingForActivity); err != nil {
			return Movement{}, err
		}
		if in.Quantity > outstandingForActivity {
			return Movement{}, ErrReturnExceedsLoaned
		}
	default:
		// Every other type consumes existing available stock.
		var available int32
		if err := tx.QueryRow(ctx, `
			SELECT available_quantity FROM material_item_quantities WHERE item_id = $1
		`, in.ItemID).Scan(&available); err != nil {
			return Movement{}, err
		}
		if available < in.Quantity {
			return Movement{}, ErrInsufficientStock
		}
	}

	// Rental stays open until returned (see ReturnMovement); every
	// other type — loan and return included — is settled the moment
	// it's recorded. A loan's own "returned" state is no longer tracked
	// on the loan itself; it's the sum of separate 'return' movements
	// for the same item and activity.
	status := MovementCompleted
	if in.Type == MovementRental {
		status = MovementActive
	}

	var m Movement
	err = tx.QueryRow(ctx, `
		INSERT INTO material_movements (item_id, user_id, type, quantity, transacted_at, price, status, recorded_by, activity_id, notes)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		RETURNING id, item_id, user_id, type, quantity, transacted_at, returned_at,
			price, status, recorded_by, activity_id, notes
	`, in.ItemID, in.UserID, in.Type, in.Quantity, in.TransactedAt, price, status, in.RecordedBy, in.ActivityID, in.Notes,
	).Scan(&m.ID, &m.ItemID, &m.UserID, &m.Type, &m.Quantity, &m.TransactedAt,
		&m.ReturnedAt, &m.Price, &m.Status, &m.RecordedBy, &m.ActivityID, &m.Notes)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.ConstraintName == activityRequiredConstraint {
		return Movement{}, ErrActivityRequired
	}
	if err != nil {
		return Movement{}, err
	}

	if affectsItemStatus(in.Type) {
		if err := r.syncItemStatusAfterMovement(ctx, tx, in.ItemID, in.Type); err != nil {
			return Movement{}, err
		}
	}

	return m, tx.Commit(ctx)
}

// ReturnMovement closes an active rental — the only type that still
// reaches status 'active' (every other type is 'completed' the moment
// it's recorded — see CreateMovement), always all of it at once. A
// loan's return is a separate 'return' movement instead (see
// CreateMovement), validated per item and activity rather than closing
// any one loan row.
func (r *Repository) ReturnMovement(ctx context.Context, id int64) (Movement, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Movement{}, err
	}
	defer tx.Rollback(ctx)

	var m Movement
	err = tx.QueryRow(ctx, `
		UPDATE material_movements SET returned_at = now(), status = 'completed'
		WHERE id = $1 AND status = 'active'
		RETURNING id, item_id, user_id, type, quantity, transacted_at, returned_at,
			price, status, recorded_by, activity_id, notes
	`, id).Scan(&m.ID, &m.ItemID, &m.UserID, &m.Type, &m.Quantity, &m.TransactedAt,
		&m.ReturnedAt, &m.Price, &m.Status, &m.RecordedBy, &m.ActivityID, &m.Notes)
	if errors.Is(err, pgx.ErrNoRows) {
		return Movement{}, ErrNotFound
	}
	if err != nil {
		return Movement{}, err
	}

	if _, err := tx.Exec(ctx, `UPDATE material_items SET status = 'available' WHERE id = $1`, m.ItemID); err != nil {
		return Movement{}, err
	}

	return m, tx.Commit(ctx)
}

func (r *Repository) ListMovementsByItem(ctx context.Context, itemID int64) ([]Movement, error) {
	return r.queryMovements(ctx, `WHERE m.item_id = $1 ORDER BY m.transacted_at DESC`, itemID)
}

func (r *Repository) ListMovementsByStatus(ctx context.Context, status MovementStatus) ([]Movement, error) {
	return r.queryMovements(ctx, `WHERE m.status = $1 ORDER BY m.transacted_at DESC`, status)
}

// ListMovementsByUser returns every purchase, rental and loan a person
// has ever made, regardless of status — the material history shown on
// that person's page. Acquisition/loss/abandonment/retirement/return
// rows have no user_id, so they never match here.
func (r *Repository) ListMovementsByUser(ctx context.Context, userID int64) ([]Movement, error) {
	return r.queryMovements(ctx, `WHERE m.user_id = $1 ORDER BY m.transacted_at DESC`, userID)
}

// ListMovementsByActivity returns every movement tied to an activity —
// loan and return chief among them, since that's the pairing that's
// validated against each other (see CreateMovement) rather than
// tracked via a link between specific rows. Backs the activity page's
// "material prestado" (grouped and summed client-side, item by item).
func (r *Repository) ListMovementsByActivity(ctx context.Context, activityID int64) ([]Movement, error) {
	return r.queryMovements(ctx, `WHERE m.activity_id = $1 ORDER BY m.transacted_at DESC`, activityID)
}

// queryMovements joins in the login of both the movement's recipient
// (if any) and, if recorded, whoever logged it — every caller here can
// span many different people at once (by item, by status, or even by
// user, since nothing stops the same listing shape from being reused
// later), so a JOIN is simpler than resolving each id after the fact
// the way the two single-row RETURNING writes above have to.
func (r *Repository) queryMovements(ctx context.Context, whereClause string, arg any) ([]Movement, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT m.id, m.item_id, m.user_id, u.login, m.type, m.quantity, m.transacted_at,
			m.returned_at, m.price, m.status, m.recorded_by, ru.login, m.activity_id, m.notes
		FROM material_movements m
		LEFT JOIN users u ON u.id = m.user_id
		LEFT JOIN users ru ON ru.id = m.recorded_by
		`+whereClause, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []Movement{}
	for rows.Next() {
		var m Movement
		if err := rows.Scan(&m.ID, &m.ItemID, &m.UserID, &m.UserLogin, &m.Type, &m.Quantity, &m.TransactedAt,
			&m.ReturnedAt, &m.Price, &m.Status, &m.RecordedBy, &m.RecordedByLogin, &m.ActivityID, &m.Notes); err != nil {
			return nil, err
		}
		list = append(list, m)
	}
	return list, rows.Err()
}
