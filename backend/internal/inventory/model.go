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
	ID                int64      `json:"id"`
	Name              string     `json:"name"`
	Description       *string    `json:"description,omitempty"`
	Category          *string    `json:"category,omitempty"`
	Code              *string    `json:"code,omitempty"`
	TotalQuantity     int32      `json:"total_quantity"`
	AvailableQuantity int32      `json:"available_quantity"`
	Status            ItemStatus `json:"status"`
	RentalPricePerDay *float64   `json:"rental_price_per_day,omitempty"`
	SalePrice         *float64   `json:"sale_price,omitempty"`
}

type CreateItemInput struct {
	Name              string   `json:"name"`
	Description       *string  `json:"description"`
	Category          *string  `json:"category"`
	Code              *string  `json:"code"`
	TotalQuantity     int32    `json:"total_quantity"`
	RentalPricePerDay *float64 `json:"rental_price_per_day"`
	SalePrice         *float64 `json:"sale_price"`
}

type Transaction struct {
	ID           int64             `json:"id"`
	ItemID       int64             `json:"item_id"`
	UserID       int64             `json:"user_id"`
	Type         TransactionType   `json:"type"`
	Quantity     int32             `json:"quantity"`
	TransactedAt time.Time         `json:"transacted_at"`
	DueDate      *time.Time        `json:"due_date,omitempty"`
	ReturnedAt   *time.Time        `json:"returned_at,omitempty"`
	Price        *float64          `json:"price,omitempty"`
	Status       TransactionStatus `json:"status"`
	RecordedBy   *int64            `json:"recorded_by,omitempty"`
}

type CreateTransactionInput struct {
	ItemID     int64
	UserID     int64
	Type       TransactionType
	Quantity   int32
	DueDate    *time.Time
	Price      *float64
	RecordedBy *int64
}
