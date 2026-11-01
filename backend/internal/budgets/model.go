// Package budgets holds the club's yearly budgets. A budget covers one calendar year and has two
// parts, income and expenses, each made of line items ("partidas") with an estimated amount. A line
// item stands for one or more accounts of the part's type. The budget of a year must cover every
// income and expense account of the ledger, and its two parts must add up to the same amount.
// Everybody who can sign in reads budgets (with what was really received or spent per item, and the
// percentage reached); only admin and money change them. See migration 00039_budgets.sql.
package budgets

import "errors"

type Kind string

const (
	KindIncome  Kind = "income"
	KindExpense Kind = "expense"
)

var (
	ErrNotFound           = errors.New("budget not found")
	ErrInvalidYear        = errors.New("year must be between 1900 and 2200")
	ErrInvalidItem        = errors.New("every line item needs a name and an amount of zero or more, with at most two decimals")
	ErrDuplicateItem      = errors.New("two line items of the same part have the same name")
	ErrItemWithoutAccount = errors.New("every line item needs at least one account")
	ErrUnknownAccount     = errors.New("unknown account")
	ErrNotBudgetable      = errors.New("only accounts that hold transactions (not groups) can be in a budget")
	ErrTypeMismatch       = errors.New("an account's type must match the part of the line item it is in")
	ErrDuplicateAccount   = errors.New("an account can only be in one line item")
	ErrAccountsMissing    = errors.New("accounts missing")
	ErrUnbalanced         = errors.New("income and expenses must add up to the same amount")
)

// AccountRef is an account as a budget shows it.
type AccountRef struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Code   string `json:"code,omitempty"`
	Hidden bool   `json:"hidden"`
}

// Item is a line item as the API shows it. Actual is what was really received (income) or spent
// (expense) in the year by its accounts; Percent is Actual / Amount, nil when Amount is zero.
type Item struct {
	Name     string       `json:"name"`
	Amount   float64      `json:"amount"`
	Actual   float64      `json:"actual"`
	Percent  *float64     `json:"percent"`
	Accounts []AccountRef `json:"accounts"`
}

// Part is the income or the expense half of a budget.
type Part struct {
	Items   []Item   `json:"items"`
	Amount  float64  `json:"amount"`
	Actual  float64  `json:"actual"`
	Percent *float64 `json:"percent"`
}

// UnassignedAccount is a leaf income/expense account that the saved budget does not cover (it was
// created afterwards), with what it moved in the year.
type UnassignedAccount struct {
	AccountRef
	Kind   Kind    `json:"kind"`
	Actual float64 `json:"actual"`
}

// Budget is the whole budget of a year.
type Budget struct {
	Year               int                 `json:"year"`
	Income             Part                `json:"income"`
	Expenses           Part                `json:"expenses"`
	UnassignedAccounts []UnassignedAccount `json:"unassigned_accounts"`
}

// Summary is a budget in the list of years.
type Summary struct {
	Year            int      `json:"year"`
	IncomeAmount    float64  `json:"income_amount"`
	IncomeActual    float64  `json:"income_actual"`
	IncomePercent   *float64 `json:"income_percent"`
	ExpensesAmount  float64  `json:"expenses_amount"`
	ExpensesActual  float64  `json:"expenses_actual"`
	ExpensesPercent *float64 `json:"expenses_percent"`
}

// ItemInput is a line item of a PUT body; the amount is in cents.
type ItemInput struct {
	Name       string
	Cents      int64
	AccountIDs []int64
}

// Input is the whole budget of a year as saved.
type Input struct {
	Income   []ItemInput
	Expenses []ItemInput
}
