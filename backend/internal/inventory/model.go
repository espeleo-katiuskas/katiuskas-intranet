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
	ID                 int64      `json:"id"`
	Name               string     `json:"name"`
	Description        *string    `json:"description,omitempty"`
	Category           *string    `json:"category,omitempty"`
	Code               *string    `json:"code,omitempty"`
	TotalQuantity      int32      `json:"total_quantity"`
	AvailableQuantity  int32      `json:"available_quantity"`
	Status             ItemStatus `json:"status"`
	RentalPricePerWeek *float64   `json:"rental_price_per_week,omitempty"`
	SalePrice          *float64   `json:"sale_price,omitempty"`
}

type CreateItemInput struct {
	Name               string   `json:"name"`
	Description        *string  `json:"description"`
	Category           *string  `json:"category"`
	Code               *string  `json:"code"`
	TotalQuantity      int32    `json:"total_quantity"`
	RentalPricePerWeek *float64 `json:"rental_price_per_week"`
	SalePrice          *float64 `json:"sale_price"`
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
	UserLogin    string            `json:"user_login"`
	Type         TransactionType   `json:"type"`
	Quantity     int32             `json:"quantity"`
	TransactedAt time.Time         `json:"transacted_at"`
	DueDate      *time.Time        `json:"due_date,omitempty"`
	ReturnedAt   *time.Time        `json:"returned_at,omitempty"`
	Price        *float64          `json:"price,omitempty"`
	Status       TransactionStatus `json:"status"`
	RecordedBy      *int64  `json:"-"`
	RecordedByLogin *string `json:"recorded_by_login,omitempty"`
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
