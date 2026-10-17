package distributions

import "time"

// Status is where a distribution is in its life, each state being a different
// thing people can do: draft (being prepared, editable, only admin/board see
// it), open (members ask for units and choose their options), closed (nothing
// changes any more, and nothing can be handed out yet), delivering (the units
// are being handed out) and done. The values are the distribution_status enum
// (migrations 00021 and 00023), in order.
type Status string

const (
	StatusDraft      Status = "draft"
	StatusOpen       Status = "open"
	StatusClosed     Status = "closed"
	StatusDelivering Status = "delivering"
	StatusDone       Status = "done"
)

var statusOrder = []Status{StatusDraft, StatusOpen, StatusClosed, StatusDelivering, StatusDone}

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

// Option is something to choose when asking for the article — "Talla", with
// the values it can take.
type Option struct {
	Name   string   `json:"name"`
	Values []string `json:"values"`
}

// Distribution is one article the club hands out to its members, with what
// it costs and what has to be chosen. Price is for a member's own unit (0 =
// free); ExtraPrice, when present, is what each extra unit costs and means
// extras exist at all; MaxExtra caps them per person (nil = no cap).
// Eligible is only filled in for one particular caller by the handler — it
// isn't stored — and says whether that person may have one.
type Distribution struct {
	ID             int64      `json:"id"`
	Name           string     `json:"name"`
	Description    *string    `json:"description,omitempty"`
	Audience       Audience   `json:"audience"`
	Status         Status     `json:"status"`
	ClosesAt       *time.Time `json:"closes_at,omitempty"`
	CutoffDate     *time.Time `json:"cutoff_date,omitempty"`
	Price          float64    `json:"price"`
	ExtraPrice     *float64   `json:"extra_price,omitempty"`
	MaxExtra       *int       `json:"max_extra,omitempty"`
	Options        []Option   `json:"options"`
	CreatedByLogin string     `json:"created_by_login"`
	CreatedAt      time.Time  `json:"created_at"`
	UnitCount      int        `json:"unit_count"`
	PickedUpCount  int        `json:"picked_up_count"`
	Eligible       *bool      `json:"eligible,omitempty"`
}

// Unit is one unit somebody wants: for BeneficiaryLogin, entered by
// RegisteredByLogin at RegisteredAt — a different person when a parent signs
// a child up or a friend adds an extra for another, so it stays clear who
// actually did it. In a distribution for all members each member's own unit
// exists without anybody entering it: RegisteredByLogin is then absent until
// somebody chooses its options, and with options to choose, empty Choices
// means nobody has chosen yet. Price is what it cost when it was entered. Collection
// carries three facts: when, who wrote it down (admin, board or material) and
// who it was handed to, which may not be the beneficiary.
type Unit struct {
	ID                    int64             `json:"id"`
	DistributionID        int64             `json:"distribution_id"`
	BeneficiaryID         int64             `json:"-"`
	BeneficiaryLogin      string            `json:"beneficiary_login"`
	BeneficiaryName       string            `json:"beneficiary_name"`
	RegisteredByID        *int64            `json:"-"`
	RegisteredByLogin     *string           `json:"registered_by_login,omitempty"`
	RegisteredAt          time.Time         `json:"registered_at"`
	IsExtra               bool              `json:"is_extra"`
	Choices               map[string]string `json:"choices"`
	Price                 float64           `json:"price"`
	PaidAt                *time.Time        `json:"paid_at,omitempty"`
	PaidMarkedByLogin     *string           `json:"paid_marked_by_login,omitempty"`
	PickedUpAt            *time.Time        `json:"picked_up_at,omitempty"`
	PickupRecordedByLogin *string           `json:"pickup_recorded_by_login,omitempty"`
	PickedUpByLogin       *string           `json:"picked_up_by_login,omitempty"`
}

// OptionInput is an Option as given when creating or editing a distribution.
type OptionInput struct {
	Name   string
	Values []string
}

// CreateInput is what creating a distribution takes. A new one always starts
// as a draft.
type CreateInput struct {
	Name        string
	Description *string
	Audience    Audience
	ClosesAt    *time.Time
	CutoffDate  *time.Time
	Price       float64
	ExtraPrice  *float64
	MaxExtra    *int
	Options     []OptionInput
	CreatedBy   int64
}

// UpdateInput replaces every field of CreateInput (except who created it)
// wholesale, like accounting's own updates do, so a field left out is turned
// off. Options and Status are the exceptions: nil leaves them as they are.
// The options can only be replaced while nobody has asked for anything yet,
// since each unit's choices are values of them; sending back what is already
// there is not a replacement.
type UpdateInput struct {
	Name        string
	Description *string
	Audience    Audience
	ClosesAt    *time.Time
	CutoffDate  *time.Time
	Price       float64
	ExtraPrice  *float64
	MaxExtra    *int
	Options     *[]OptionInput
	Status      *Status
}

// RegisterInput is one unit being asked for. RegisteredByID is whoever is
// really doing it. Staff (admin/board) may keep entering units after the
// deadline, and while the distribution is closed.
type RegisterInput struct {
	DistributionID int64
	BeneficiaryID  int64
	RegisteredByID int64
	IsExtra        bool
	Choices        map[string]string
	Staff          bool
}

// OrderLine is what has to be bought for one combination of choices.
type OrderLine struct {
	Choices map[string]string `json:"choices"`
	Count   int               `json:"count"`
}

// Order is the summary for placing the order: how many units of each
// combination of choices (a line with empty choices, for an article with
// options, is the units nobody has chosen yet) and the totals.
type Order struct {
	Lines       []OrderLine `json:"lines"`
	TotalUnits  int         `json:"total_units"`
	TotalAmount float64     `json:"total_amount"`
}
