package distributions

import (
	"fmt"
	"time"
)

// Status is where a distribution is in its life, each state being a different
// thing people can do: draft (being prepared, editable, only admin/board see
// it), open (members ask for units and choose their options), closed (nothing
// can be asked for any more) and done (finished). Units can be handed out in any
// state but draft — the order often arrives before the asking has ended, and
// somebody may still come for theirs once it is done.
// The values are the distribution_status enum (migrations 00021, 00023 and
// 00024), in order.
type Status string

const (
	StatusDraft  Status = "draft"
	StatusOpen   Status = "open"
	StatusClosed Status = "closed"
	StatusDone   Status = "done"
)

var statusOrder = []Status{StatusDraft, StatusOpen, StatusClosed, StatusDone}

// Deliverable says whether units can be handed out (collections recorded, numbers
// written down) in this state.
func (s Status) Deliverable() bool { return s != StatusDraft }

func (s Status) Valid() bool { return s.index() >= 0 }

func (s Status) index() int {
	for i, v := range statusOrder {
		if v == s {
			return i
		}
	}
	return -1
}

// Audience says who a distribution is for: every member on its cutoff date,
// or only those who sign up (still limited to members on the cutoff date when
// one is set).
type Audience string

const (
	AudienceAllMembers Audience = "all_members"
	AudienceSignup     Audience = "signup"
)

func (a Audience) Valid() bool { return a == AudienceAllMembers || a == AudienceSignup }

// Visibility says who can see a distribution, and so act on it at all: everybody
// (the default), or only material, board and admin; board and admin; or admin
// alone. Each level includes the ones above it (admin can do anything board can,
// and board anything material can). Only admin changes it.
type Visibility string

const (
	VisibilityAll      Visibility = "all"
	VisibilityMaterial Visibility = "material"
	VisibilityBoard    Visibility = "board"
	VisibilityAdmin    Visibility = "admin"
)

func (v Visibility) Valid() bool {
	return v == VisibilityAll || v == VisibilityMaterial || v == VisibilityBoard || v == VisibilityAdmin
}

// Option is something to choose when asking for the article — "Talla", with
// the values it can take.
type Option struct {
	Name   string   `json:"name"`
	Values []string `json:"values"`
}

// Article is one of the things a distribution hands out in any quantity — the
// Christmas lottery has two, "papeletas" and "décimos". Price is what one unit
// costs; MaxTotal, when present, is how many units there are to give out in all
// (nil = no limit); Requested is how many have been asked for so far, so
// whoever looks can tell how many are left.
type Article struct {
	ID       int64   `json:"id"`
	Name     string  `json:"name"`
	Price    float64 `json:"price"`
	MaxTotal *int    `json:"max_total,omitempty"`
	// MaxPerPerson caps how many of it one person may ask for (nil = no cap).
	MaxPerPerson *int `json:"max_per_person,omitempty"`
	// HasIdentifiers means each unit has its own number (a serial number, a
	// series and fraction), written down when the units are handed out.
	HasIdentifiers bool `json:"has_identifiers"`
	// IdentifierFields describe the numbers the items can have (see
	// IdentifierField), so every one that exists is known: IdentifierTotal of
	// them, IdentifiersAssigned already given to somebody. Empty means numbers
	// are free text. With them MaxTotal is always IdentifierTotal.
	IdentifierFields    []IdentifierField `json:"identifier_fields"`
	IdentifierTotal     int               `json:"identifier_total,omitempty"`
	IdentifiersAssigned int               `json:"identifiers_assigned,omitempty"`
	Requested           int               `json:"requested"`
}

// String is how an article reads in a notification of what changed: without
// Requested, which moves as people ask and isn't something anybody edits.
func (a Article) String() string {
	s := fmt.Sprintf("%s %.2f €", a.Name, a.Price)
	if a.MaxTotal != nil {
		s += fmt.Sprintf(" (máx. %d)", *a.MaxTotal)
	}
	if a.MaxPerPerson != nil {
		s += fmt.Sprintf(" (%d por persona)", *a.MaxPerPerson)
	}
	if a.HasIdentifiers {
		s += " (con números)"
	}
	for _, f := range a.IdentifierFields {
		s += fmt.Sprintf(" [%s: %s]", f.Name, f.Spec)
	}
	return s
}

// Distribution is one article the club hands out to its members, with what
// it costs and what has to be chosen. Price is for a member's own unit (0 =
// free); ExtraPrice, when present, is what each extra unit costs and means
// extras exist at all; MaxExtra caps them per person (nil = no cap).
// A distribution with Articles is a different kind: its price, extras and
// options are not used, and people ask for any quantity of each article
// instead of having one own unit (see Article). Eligible is only filled in for one particular caller by the handler — it
// isn't stored — and says whether that person may have one.
type Distribution struct {
	ID             int64      `json:"id"`
	Name           string     `json:"name"`
	Description    *string    `json:"description,omitempty"`
	Audience       Audience   `json:"audience"`
	Visibility     Visibility `json:"visibility"`
	Status         Status     `json:"status"`
	ClosesAt       *time.Time `json:"closes_at,omitempty"`
	CutoffDate     *time.Time `json:"cutoff_date,omitempty"`
	Price          float64    `json:"price"`
	ExtraPrice     *float64   `json:"extra_price,omitempty"`
	MaxExtra       *int       `json:"max_extra,omitempty"`
	Options        []Option   `json:"options"`
	Articles       []Article  `json:"articles"`
	CreatedByLogin string     `json:"created_by_login"`
	CreatedAt      time.Time  `json:"created_at"`
	// AccountID is the account its payments are recorded in (see migration
	// 00032_distribution_accounts.sql), absent when it uses the one marked as the
	// default for distributions; AccountName is that account's name.
	AccountID   *int64  `json:"account_id,omitempty"`
	AccountName *string `json:"account_name,omitempty"`
	// UnitCount and PickedUpCount are in units, not rows: a row of an article
	// stands for as many as its quantity.
	UnitCount     int   `json:"unit_count"`
	PickedUpCount int   `json:"picked_up_count"`
	Eligible      *bool `json:"eligible,omitempty"`
}

// Unit is one unit somebody wants: for BeneficiaryLogin, entered by
// RegisteredByLogin at RegisteredAt — a different person when a parent signs
// a child up or a friend adds an extra for another, so it stays clear who
// actually did it. In a distribution for all members each member's own unit
// exists without anybody entering it: RegisteredByLogin is then absent until
// somebody chooses its options, and with options to choose, empty Choices
// means nobody has chosen yet. A unit of an article (ArticleID) stands for
// Quantity of them, with no options and never an extra. Price is what ONE unit
// cost when it was entered, so a row costs Price times Quantity. Collection
// carries three facts: when, who wrote it down (admin, board or material) and
// who it was handed to, which may not be the beneficiary.
type Unit struct {
	ID                int64     `json:"id"`
	DistributionID    int64     `json:"distribution_id"`
	BeneficiaryID     int64     `json:"-"`
	BeneficiaryLogin  string    `json:"beneficiary_login"`
	BeneficiaryName   string    `json:"beneficiary_name"`
	RegisteredByID    *int64    `json:"-"`
	RegisteredByLogin *string   `json:"registered_by_login,omitempty"`
	RegisteredAt      time.Time `json:"registered_at"`
	IsExtra           bool      `json:"is_extra"`
	ArticleID         *int64    `json:"article_id,omitempty"`
	ArticleName       *string   `json:"article_name,omitempty"`
	// Identifiers are the numbers of the items handed out, for a row of an
	// article that has them.
	Identifiers []string `json:"identifiers,omitempty"`
	// DeliveredIdentifiers are those of Identifiers already handed out; the rest
	// are assigned but still to be delivered.
	DeliveredIdentifiers []string `json:"delivered_identifiers,omitempty"`
	// PaidIdentifiers are those of Identifiers already paid: in an article with
	// numbers it is the items that are paid, and only those with a number can be.
	PaidIdentifiers []string          `json:"paid_identifiers,omitempty"`
	Quantity        int               `json:"quantity"`
	Choices         map[string]string `json:"choices"`
	Price           float64           `json:"price"`
	// PaidQuantity is how many of the unit's Quantity have been paid (a row of
	// units can be paid in part); PaidAt and PaidMarkedByLogin are when and by
	// whom the last payment was marked, and are there when something is paid.
	PaidQuantity      int        `json:"paid_quantity"`
	PaidAt            *time.Time `json:"paid_at,omitempty"`
	PaidMarkedByLogin *string    `json:"paid_marked_by_login,omitempty"`
	// PickedUpQuantity is how many of the unit's Quantity were handed out (a row
	// can be collected in part); PickedUpAt and the two people after it are those of
	// the last delivery, and are there when something was handed out.
	PickedUpQuantity      int        `json:"picked_up_quantity"`
	PickedUpAt            *time.Time `json:"picked_up_at,omitempty"`
	PickupRecordedByLogin *string    `json:"pickup_recorded_by_login,omitempty"`
	PickedUpByLogin       *string    `json:"picked_up_by_login,omitempty"`
}

// Person is somebody a distribution can be asked for or collected by, as the
// pickers offer them: a name to read and a login to send back.
type Person struct {
	Login     string `json:"login"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}

// OptionInput is an Option as given when creating or editing a distribution.
type OptionInput struct {
	Name   string
	Values []string
}

// ArticleInput is an Article as given when creating or editing a distribution.
type ArticleInput struct {
	Name             string
	Price            float64
	MaxTotal         *int
	MaxPerPerson     *int
	HasIdentifiers   bool
	IdentifierFields []IdentifierField
}

// CreateInput is what creating a distribution takes. A new one always starts
// as a draft.
type CreateInput struct {
	Name        string
	Description *string
	Audience    Audience
	// Visibility defaults to all when empty.
	Visibility Visibility
	ClosesAt   *time.Time
	CutoffDate *time.Time
	Price      float64
	ExtraPrice *float64
	MaxExtra   *int
	Options    []OptionInput
	Articles   []ArticleInput
	AccountID  *int64
	CreatedBy  int64
}

// UpdateInput replaces every field of CreateInput (except who created it)
// wholesale, like accounting's own updates do, so a field left out is turned
// off. Options, Visibility and Status are the exceptions: nil leaves them as they are.
// The options can only be replaced while nobody has asked for anything yet,
// since each unit's choices are values of them; sending back what is already
// there is not a replacement. Articles work the same way (nil leaves them), and
// once somebody has asked for something only the limits of each (MaxTotal,
// MaxPerPerson) and whether it HasIdentifiers can change — the limits to no less
// than what has already been asked for.
type UpdateInput struct {
	Name        string
	Description *string
	Audience    Audience
	// Visibility nil leaves it as it is.
	Visibility *Visibility
	ClosesAt   *time.Time
	CutoffDate *time.Time
	Price      float64
	ExtraPrice *float64
	MaxExtra   *int
	Options    *[]OptionInput
	Articles   *[]ArticleInput
	AccountID  *int64
	Status     *Status
}

// RegisterInput is one unit being asked for. RegisteredByID is whoever is
// really doing it. Staff (admin, board and material: who put the order together
// and hand it out) may enter units after the deadline and while the
// distribution is closed or done, though never in a draft.
type RegisterInput struct {
	DistributionID int64
	BeneficiaryID  int64
	RegisteredByID int64
	IsExtra        bool
	Choices        map[string]string
	// ArticleID and Quantity are for a distribution with articles, and only for
	// those: which article, and how many of it.
	ArticleID *int64
	Quantity  int
	Staff     bool
}

// OrderLine is what has to be bought for one combination of choices — of one
// article, in a distribution that has them.
type OrderLine struct {
	ArticleID   *int64            `json:"article_id,omitempty"`
	ArticleName *string           `json:"article_name,omitempty"`
	Choices     map[string]string `json:"choices"`
	Count       int               `json:"count"`
}

// Order is the summary for placing the order: how many units of each
// combination of choices (a line with empty choices, for an article with
// options, is the units nobody has chosen yet) and the totals. Provisional
// means the distribution is still a draft and, being for all members, the
// own unit of everybody who qualifies is counted although it isn't made yet
// (it is when the distribution opens).
type Order struct {
	Lines       []OrderLine `json:"lines"`
	TotalUnits  int         `json:"total_units"`
	TotalAmount float64     `json:"total_amount"`
	Provisional bool        `json:"provisional"`
}

// NumberHolder is who has a number: the unit it was given on and the person
// that unit is for.
type NumberHolder struct {
	UnitID    int64
	Login     string
	Name      string
	Delivered bool
	Paid      bool
}

// NumberItem is one item of a numbered article: its number and, when somebody
// has it, who.
type NumberItem struct {
	Identifier       string  `json:"identifier"`
	UnitID           *int64  `json:"unit_id,omitempty"`
	BeneficiaryLogin *string `json:"beneficiary_login,omitempty"`
	BeneficiaryName  *string `json:"beneficiary_name,omitempty"`
	Delivered        bool    `json:"delivered,omitempty"`
	Paid             bool    `json:"paid,omitempty"`
}

// Numbers is every item that exists of a numbered article, in order, with who
// has each, and how many are given and free.
type Numbers struct {
	ArticleID int64             `json:"article_id"`
	Fields    []IdentifierField `json:"fields"`
	Total     int               `json:"total"`
	Assigned  int               `json:"assigned"`
	Free      int               `json:"free"`
	Items     []NumberItem      `json:"items"`
}

// Payment is some units of a row marked as paid: Quantity of them, or all that
// are still to be paid when it is 0 — in an article with numbers only the ones
// with a number can be paid, the given Identifiers or else the first.
type Payment struct {
	UnitID      int64
	Quantity    int
	Identifiers []string
}

// Delivery is some units of a row handed out: Quantity of them (all that are
// still to be handed out when it is 0) or, for an article with numbers, the
// given Identifiers, which have to be assigned to the row and not delivered yet.
// Without Identifiers, the first assigned ones that are not delivered go.
type Delivery struct {
	UnitID      int64
	Quantity    int
	Identifiers []string
}

// PaymentEntry is one entry of the list of payments: some units of an order marked
// as paid (Quantity and Amount positive) or a payment taken off (negative), who
// wrote it down and when, whose order it was for — the person who paid — what it
// was and which numbers. TotalAfter is what the distribution had collected
// up to and including this entry.
type PaymentEntry struct {
	ID               int64     `json:"id"`
	DistributionID   int64     `json:"distribution_id"`
	DistributionName string    `json:"distribution_name"`
	UnitID           *int64    `json:"unit_id,omitempty"`
	OccurredAt       time.Time `json:"occurred_at"`
	RecordedByLogin  string    `json:"recorded_by_login"`
	RecordedByName   string    `json:"recorded_by_name"`
	PaidByLogin      string    `json:"paid_by_login"`
	PaidByName       string    `json:"paid_by_name"`
	Description      string    `json:"description"`
	Quantity         int       `json:"quantity"`
	Amount           float64   `json:"amount"`
	Identifiers      []string  `json:"identifiers"`
	// TransactionID is the accounting transaction that recorded it, absent for the
	// payments made before they were recorded there.
	TransactionID *int64  `json:"transaction_id,omitempty"`
	TotalAfter    float64 `json:"total_after"`
}

// PaymentFilter narrows and pages the list of payments: one distribution, an
// inclusive range of days.
type PaymentFilter struct {
	DistributionID *int64
	From, To       *time.Time
	Limit, Offset  int
}
