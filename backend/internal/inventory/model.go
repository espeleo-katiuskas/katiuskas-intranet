// Package inventory manages club gear available for loan, rental or sale,
// and every movement recorded against it.
package inventory

import "time"

type ItemStatus string

const (
	ItemAvailable   ItemStatus = "available"
	ItemOnLoan      ItemStatus = "on_loan"
	ItemRented      ItemStatus = "rented"
	ItemSold        ItemStatus = "sold"
	ItemLost        ItemStatus = "lost"
	ItemAbandoned   ItemStatus = "abandoned"
	ItemMaintenance ItemStatus = "maintenance"
	ItemRetired     ItemStatus = "retired"
)

// ItemType distinguishes the three shapes an inventory item can take —
// see migration 00013. Rope and bag are individually tracked physical
// objects (their own code, always total_quantity 1) with type-specific
// attributes; gear is everything else, a fungible stock item filed
// under an admin-managed category.
type ItemType string

const (
	ItemTypeRope ItemType = "rope"
	ItemTypeBag  ItemType = "bag"
	ItemTypeGear ItemType = "gear"
)

// RopeType is a rope's discipline: caving over rock and mud, canyoning
// through water — the two disciplines the club's federative license
// (licenses.Type.RequiresFederation) also distinguishes.
type RopeType string

const (
	RopeTypeCaving    RopeType = "caving"
	RopeTypeCanyoning RopeType = "canyoning"
)

// BagType is a saca's kind: the same caving/canyoning split as
// RopeType, plus two purpose-based kinds that don't map to a
// discipline — quick (a bag kept ready for fast access) and special
// (anything else purpose-built).
type BagType string

const (
	BagTypeCaving    BagType = "caving"
	BagTypeCanyoning BagType = "canyoning"
	BagTypeQuick     BagType = "quick"
	BagTypeSpecial   BagType = "special"
)

// MaterialCategory is one entry in the gear category catalog — kept in
// the database rather than a Go enum, the same license_types pattern
// (licenses.LicenseType), so the list can grow with an INSERT instead
// of a code deploy.
type MaterialCategory struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// MovementType is either an exchange with a specific person (loan,
// rental, sale — each requires a UserID) or a stock-level change with
// no recipient: acquisition brings new units in, loss/abandonment/
// retirement permanently take units out (lost in the field, left
// behind on a trip, or decommissioned). See Repository.CreateMovement
// for how each type affects an item's computed quantities and status.
type MovementType string

const (
	MovementLoan        MovementType = "loan"
	MovementRental      MovementType = "rental"
	MovementSale        MovementType = "sale"
	MovementAcquisition MovementType = "acquisition"
	MovementLoss        MovementType = "loss"
	MovementAbandonment MovementType = "abandonment"
	MovementRetirement  MovementType = "retirement"
)

type MovementStatus string

const (
	MovementActive    MovementStatus = "active"
	MovementCompleted MovementStatus = "completed"
	MovementOverdue   MovementStatus = "overdue"
	MovementCancelled MovementStatus = "cancelled"
)

type Item struct {
	ID   int64    `json:"id"`
	Type ItemType `json:"item_type"`
	// Brand/Model are required for rope and bag (they identify a
	// specific physical object) and optional for gear — some parts are
	// generic, or the brand simply isn't known.
	Brand *string `json:"brand,omitempty"`
	Model *string `json:"model,omitempty"`
	// Code is required for rope and bag, optional for gear.
	Code *string `json:"code,omitempty"`
	// Description is required for gear (it may be the only thing that
	// identifies an item with no known brand/model) and doubles as
	// "Observaciones" in the frontend for rope/bag, where it's optional
	// — same field underneath, just labelled differently per type.
	Description *string `json:"description,omitempty"`
	// TotalQuantity/AvailableQuantity are never stored — they're
	// computed from this item's own movement history (see the
	// inventory_item_quantities view and Repository.itemSelect), so
	// they can never drift from what actually happened. There's no
	// AcquiredAt field for the same reason: it's the transacted_at of
	// this item's first 'acquisition' movement, visible in its
	// movement history instead of being duplicated here.
	TotalQuantity     int32      `json:"total_quantity"`
	AvailableQuantity int32      `json:"available_quantity"`
	Status            ItemStatus `json:"status"`
	// Loanable says whether this item can be loaned out at all —
	// Rentable/Sellable below need no such flag, since they're fully
	// determined by whether a price is set, but a loan carries no price
	// to derive that from.
	Loanable           bool     `json:"loanable"`
	Rentable           bool     `json:"rentable"`
	Sellable           bool     `json:"sellable"`
	RentalPricePerWeek *float64 `json:"rental_price_per_week,omitempty"`
	SalePrice          *float64 `json:"sale_price,omitempty"`
	// Rope-only.
	RopeType        *RopeType `json:"rope_type,omitempty"`
	DiameterMM      *float64  `json:"diameter_mm,omitempty"`
	ManufactureYear *int32    `json:"manufacture_year,omitempty"`
	LengthM         *int32    `json:"length_m,omitempty"`
	// Bag-only.
	BagType        *BagType `json:"bag_type,omitempty"`
	CapacityLiters *float64 `json:"capacity_liters,omitempty"`
	// Gear-only. CategoryID is the catalog id; CategoryName is resolved
	// alongside it (see Repository.queryItems/CreateItem), the same
	// LicenseTypeID/Type split licenses.License uses.
	CategoryID   *int64  `json:"category_id,omitempty"`
	CategoryName *string `json:"category_name,omitempty"`
}

type CreateItemInput struct {
	Type        ItemType
	Brand       *string
	Model       *string
	Code        *string
	Description *string
	// InitialQuantity is how many units the item starts with — recorded
	// as its first 'acquisition' movement rather than stored on the
	// item row (see Repository.CreateItem). AcquiredAt backdates that
	// movement; a nil value defaults it to today, same as any other
	// movement.
	InitialQuantity    int32
	AcquiredAt         *time.Time
	Loanable           bool
	RentalPricePerWeek *float64
	SalePrice          *float64
	RopeType           *RopeType
	DiameterMM         *float64
	ManufactureYear    *int32
	LengthM            *int32
	BagType            *BagType
	CapacityLiters     *float64
	CategoryID         *int64
}

type Movement struct {
	ID     int64 `json:"id"`
	ItemID int64 `json:"item_id"`
	// UserID/UserLogin are nil for acquisition, loss, abandonment and
	// retirement, which have no recipient — see MovementType. They're
	// always set for loan/rental/sale.
	UserID *int64 `json:"-"`
	// UserLogin/RecordedByLogin are filled in either by
	// Repository.queryMovements' JOIN (which can span many different
	// people across a single listing) or, for the two writes that use
	// INSERT/UPDATE ... RETURNING (no JOIN available there), by the
	// handler afterward — see licenses.Handler for the same split.
	UserLogin *string      `json:"user_login,omitempty"`
	Type      MovementType `json:"type"`
	Quantity  int32        `json:"quantity"`
	// TransactedAt defaults to today (see CreateMovementInput) but can
	// be backdated — whoever logs the movement may be doing so after
	// the fact.
	TransactedAt    time.Time      `json:"transacted_at"`
	ReturnedAt      *time.Time     `json:"returned_at,omitempty"`
	Price           *float64       `json:"price,omitempty"`
	Status          MovementStatus `json:"status"`
	RecordedBy      *int64         `json:"-"`
	RecordedByLogin *string        `json:"recorded_by_login,omitempty"`
	// ActivityID is required for loan and rental (they always happen as
	// part of an activity) and optional for every other type.
	ActivityID *int64 `json:"activity_id,omitempty"`
	// Notes is free text, mainly useful on loss/abandonment/retirement
	// to say why ("cuerda gastada", "se quedó instalada en la sima X").
	Notes *string `json:"notes,omitempty"`
}

// CreateMovementInput has no Price field: it's never given by the
// caller, since it's always the item's own catalog price for that kind
// of movement (and always nil except for rental/sale) — see
// Repository.CreateMovement.
type CreateMovementInput struct {
	ItemID       int64
	UserID       *int64
	Type         MovementType
	Quantity     int32
	TransactedAt time.Time
	RecordedBy   *int64
	ActivityID   *int64
	Notes        *string
}
