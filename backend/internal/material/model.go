// Package material manages the club's material available for loan, rental or sale,
// and every movement recorded against it.
package material

import (
	"encoding/json"
	"time"
)

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

// ItemKind distinguishes the three shapes a material item can take. Rope and bag are individually
// tracked physical objects (their own code, always total_quantity 1) with kind-specific
// attributes; general is everything else, a fungible stock item. The kind comes from the item's
// type (MaterialType): the two fixed types, Cuerda and Saca, are the rope and the bag kind, and
// every other type, which an admin edits freely, is general.
type ItemKind string

const (
	ItemKindRope    ItemKind = "rope"
	ItemKindBag     ItemKind = "bag"
	ItemKindGeneral ItemKind = "general"
)

// RopeType is a rope's discipline: caving over rock and mud, canyoning
// through water.
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

// AttributeDataType says what values an attribute accepts: any text, or one option of a list.
type AttributeDataType string

const (
	AttributeText AttributeDataType = "text"
	AttributeEnum AttributeDataType = "enum"
)

// AttributeOption is one of the values an enum attribute accepts.
type AttributeOption struct {
	ID    int64  `json:"id"`
	Label string `json:"label"`
}

// Attribute is a field the items of a general type carry (colour, closure, shape...). Its position
// is the order in which the values of an item are written in its name. The data type cannot change
// once created: the values of the items are stored by it.
type Attribute struct {
	ID       int64             `json:"id"`
	Name     string            `json:"name"`
	DataType AttributeDataType `json:"data_type"`
	Required bool              `json:"required"`
	Position int               `json:"position"`
	// Options are the accepted values of an enum attribute, by label; nil for a text one.
	Options []AttributeOption `json:"options"`
	// TypeID is the type the attribute belongs to (not in the JSON: it is listed under its type).
	TypeID int64 `json:"-"`
}

// AttributeValue is the value an item has for an attribute: in a request, AttributeID plus OptionID
// (enum) or Value (text); in a response, also the attribute's Name, and Value is always the text to
// show (the option's label for an enum).
type AttributeValue struct {
	AttributeID int64  `json:"attribute_id"`
	Name        string `json:"name,omitempty"`
	OptionID    *int64 `json:"option_id,omitempty"`
	Value       string `json:"value,omitempty"`
}

// MaterialType is one entry in the catalog of types of material (table material_types): the two
// fixed ones, Cuerda (kind rope) and Saca (kind bag), which cannot be renamed or deleted, and the
// rest (kind general), which an admin adds, renames and deletes — mosquetones, spits, placas…
// A general type has attributes, and says whether its items must have a description.
type MaterialType struct {
	ID                  int64       `json:"id"`
	Name                string      `json:"name"`
	Kind                ItemKind    `json:"kind"`
	DescriptionRequired bool        `json:"description_required"`
	Attributes          []Attribute `json:"attributes"`
}

// MovementType is either an exchange with a specific person — rental
// always requires a UserID, loan/sale/return's are optional (return is
// the other side of an earlier loan, so it may optionally name who's
// returning it too) — or a stock-level change that forbids one
// outright: acquisition brings new stock in, loss/abandonment/
// retirement permanently take units out (lost in the field, left
// behind on a trip, or decommissioned) — see recipientRequiredTypes/
// recipientForbiddenTypes in handler.go for the actual, enforced split.
// See Repository.CreateMovement for how each type affects an item's
// computed quantities and status.
type MovementType string

const (
	MovementLoan        MovementType = "loan"
	MovementRental      MovementType = "rental"
	MovementSale        MovementType = "sale"
	MovementAcquisition MovementType = "acquisition"
	MovementLoss        MovementType = "loss"
	MovementAbandonment MovementType = "abandonment"
	MovementRetirement  MovementType = "retirement"
	// MovementReturn is loaned material coming back — always tied to
	// the activity it was loaned for (like loan itself), validated
	// against how much of that item is still outstanding *for that
	// activity* rather than against any one specific loan movement
	// (see Repository.CreateMovement). Rental has no equivalent: it
	// still closes all at once via ReturnedAt/Status.
	MovementReturn MovementType = "return"
)

type MovementStatus string

const (
	MovementActive    MovementStatus = "active"
	MovementCompleted MovementStatus = "completed"
	MovementOverdue   MovementStatus = "overdue"
	MovementCancelled MovementStatus = "cancelled"
)

type Item struct {
	ID int64 `json:"id"`
	// Kind is the shape of the item (from its type); TypeID and TypeName say which type it is.
	Kind     ItemKind `json:"kind"`
	TypeID   int64    `json:"type_id"`
	TypeName string   `json:"type_name"`
	// Brand/Model are required for rope and bag (they identify a
	// specific physical object) and optional for general — some parts are
	// generic, or the brand simply isn't known.
	Brand *string `json:"brand,omitempty"`
	Model *string `json:"model,omitempty"`
	// Code is required for rope and bag, optional for general.
	Code *string `json:"code,omitempty"`
	// Description is required for a general item only if its type says so (MaterialType.
	// DescriptionRequired) and doubles as "Observaciones" in the frontend for rope/bag, where it's
	// optional — same field underneath, just labelled differently per type.
	Description *string `json:"description,omitempty"`
	// Attributes are the values the item has for its type's attributes, in the type's order; only
	// those with a value are there. Always empty for a rope and a bag.
	Attributes []AttributeValue `json:"attributes"`
	// DisplayName is how the item is named everywhere: its type, make and model, the values of its
	// attributes in order (the values only, not their names) and its description, joined with " · "
	// ("Mosquetones · Petzl · Attache · aluminio · asimétrico · con seguro").
	DisplayName string `json:"display_name"`
	// Incomplete is true for a general item that lacks what its type requires now (a required
	// attribute or the description): it was saved before the type asked for it. Editing it fixes it.
	Incomplete bool `json:"incomplete"`
	// rawAttributes is the stored jsonb, before it is turned into Attributes.
	rawAttributes map[string]json.RawMessage
	// TotalQuantity/AvailableQuantity/OutstandingLoanQuantity are never
	// stored — they're computed from this item's own movement history
	// (see the material_item_quantities view and
	// Repository.itemSelect), so they can never drift from what
	// actually happened. There's no AcquiredAt field for the same
	// reason: it's the transacted_at of this item's first 'acquisition'
	// movement, visible in its movement history instead of being
	// duplicated here.
	TotalQuantity int32 `json:"total_quantity"`
	// AvailableQuantity already reflects any outstanding loan (it's
	// subtracted, same as a rental) — OutstandingLoanQuantity is that
	// same loaned-and-not-yet-returned amount surfaced on its own,
	// summed across every activity, since it's what "material
	// prestado" is built from.
	AvailableQuantity       int32      `json:"available_quantity"`
	OutstandingLoanQuantity int32      `json:"outstanding_loan_quantity"`
	Status                  ItemStatus `json:"status"`
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
}

type CreateItemInput struct {
	TypeID      int64
	Brand       *string
	Model       *string
	Code        *string
	Description *string
	// Attributes are the values for the type's attributes; only a general item can have any.
	Attributes []AttributeValue
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
}

// UpdateItemInput is what PUT /material/items/{id} replaces: every field an item can be edited in, all at
// once (the same "resend everything" as the other PUTs), but for the type, which may be left out
// to keep it. Not the item's kind, which is what its
// shape and required fields hang from, and not its quantities, which only movements change.
type UpdateItemInput struct {
	// TypeID is the item's new type; nil keeps it. A general item can move to any other general type;
	// a rope or a bag stays Cuerda or Saca, and nothing becomes one of them (ErrKindChange).
	TypeID             *int64
	Brand              *string
	Model              *string
	Code               *string
	Description        *string
	Attributes         []AttributeValue
	Loanable           bool
	RentalPricePerWeek *float64
	SalePrice          *float64
	RopeType           *RopeType
	DiameterMM         *float64
	ManufactureYear    *int32
	LengthM            *int32
	BagType            *BagType
	CapacityLiters     *float64
}

type Movement struct {
	ID     int64 `json:"id"`
	ItemID int64 `json:"item_id"`
	// UserID/UserLogin are always nil for acquisition, loss, abandonment
	// and retirement, which never have a recipient — see MovementType.
	// They're always set for rental, and optional for loan/sale.
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
	TransactedAt time.Time `json:"transacted_at"`
	// ReturnedAt is when a rental was closed out — rental only, always
	// all-or-nothing (there's no partial rental return), and always nil
	// for every other type, loan included: a loan's "returned" state is
	// the sum of separate 'return' movements against the same item and
	// activity, not a flag on the loan itself.
	ReturnedAt      *time.Time     `json:"returned_at,omitempty"`
	Price           *float64       `json:"price,omitempty"`
	Status          MovementStatus `json:"status"`
	RecordedBy      *int64         `json:"-"`
	RecordedByLogin *string        `json:"recorded_by_login,omitempty"`
	// ActivityID is required for loan and return (both always happen as
	// part of an activity — a return is validated against how much of
	// this item that activity still has outstanding) and optional for
	// every other type, rental included — a rental isn't always tied to
	// one specific activity.
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
