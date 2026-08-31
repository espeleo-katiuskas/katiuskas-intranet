// Package inventory manages club gear available for loan, rental or sale,
// and every transaction recorded against it.
package inventory

import "time"

type ItemStatus string

const (
	ItemAvailable   ItemStatus = "available"
	ItemOnLoan      ItemStatus = "on_loan"
	ItemRented      ItemStatus = "rented"
	ItemSold        ItemStatus = "sold"
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

type TransactionType string

const (
	TransactionLoan   TransactionType = "loan"
	TransactionRental TransactionType = "rental"
	TransactionSale   TransactionType = "sale"
)

type TransactionStatus string

const (
	TransactionActive    TransactionStatus = "active"
	TransactionCompleted TransactionStatus = "completed"
	TransactionOverdue   TransactionStatus = "overdue"
	TransactionCancelled TransactionStatus = "cancelled"
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
	Description       *string    `json:"description,omitempty"`
	AcquiredAt        *time.Time `json:"acquired_at,omitempty"`
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
	Type               ItemType
	Brand              *string
	Model              *string
	Code               *string
	Description        *string
	AcquiredAt         *time.Time
	TotalQuantity      int32
	Loanable           bool
	RentalPricePerWeek *float64
	SalePrice          *float64
	RopeType           *RopeType
	DiameterMM         *float64
	ManufactureYear    *int32
	BagType            *BagType
	CapacityLiters     *float64
	CategoryID         *int64
}

type Transaction struct {
	ID     int64 `json:"id"`
	ItemID int64 `json:"item_id"`
	UserID int64 `json:"-"`
	// UserLogin/RecordedByLogin are filled in either by
	// Repository.queryTransactions' JOIN (which can span many different
	// people across a single listing) or, for the two writes that use
	// INSERT/UPDATE ... RETURNING (no JOIN available there), by the
	// handler afterward — see licenses.Handler for the same split.
	UserLogin       string            `json:"user_login"`
	Type            TransactionType   `json:"type"`
	Quantity        int32             `json:"quantity"`
	TransactedAt    time.Time         `json:"transacted_at"`
	DueDate         *time.Time        `json:"due_date,omitempty"`
	ReturnedAt      *time.Time        `json:"returned_at,omitempty"`
	Price           *float64          `json:"price,omitempty"`
	Status          TransactionStatus `json:"status"`
	RecordedBy      *int64            `json:"-"`
	RecordedByLogin *string           `json:"recorded_by_login,omitempty"`
	// ActivityID is required for loan and rental (they always happen as
	// part of an activity) and left unset for a sale.
	ActivityID *int64 `json:"activity_id,omitempty"`
}

type CreateTransactionInput struct {
	ItemID     int64
	UserID     int64
	Type       TransactionType
	Quantity   int32
	DueDate    *time.Time
	Price      *float64
	RecordedBy *int64
	ActivityID *int64
}
