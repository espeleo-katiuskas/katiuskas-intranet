// Package licenses tracks federative licenses ("licencias federativas")
// held by a person: a type — free text, not a zone-plus-modality pair,
// since real license types don't decompose cleanly ("FME", "Murciana",
// "A BASICA Infantil") — together with the date it was issued. A license
// is annual: valid from IssuedDate through December 31 of that same
// year. Only someone who currently has an open membership period can be
// assigned one; see Service.Create.
package licenses

import "time"

type License struct {
	ID         int64     `json:"id"`
	UserID     int64     `json:"user_id"`
	Type       string    `json:"type"`
	IssuedDate time.Time `json:"issued_date"`
	// ValidUntil is always December 31 of IssuedDate's year — computed,
	// never stored, since it's fully determined by IssuedDate.
	ValidUntil time.Time `json:"valid_until"`
	CreatedAt  time.Time `json:"created_at"`
}

type CreateInput struct {
	UserID     int64
	Type       string
	IssuedDate time.Time
}
