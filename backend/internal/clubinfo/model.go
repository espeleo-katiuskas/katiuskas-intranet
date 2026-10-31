// Package clubinfo is the list of "datos de interés" the home page shows — the club's name, tax
// id, address, bank account, the Bizum number — as labelled values in the database, so that an
// administrator can change them, add others and put them in order without a deploy, and so that
// nothing in them has to be written in the repository.
package clubinfo

// Item is one line of the list: a label and its value, and optionally a button that copies
// another text (the tax id without its dash, the IBAN without spaces) instead of making whoever
// reads it select the right part.
type Item struct {
	ID        int64   `json:"id"`
	Position  int     `json:"position"`
	Label     string  `json:"label"`
	Value     string  `json:"value"`
	CopyValue *string `json:"copy_value,omitempty"`
	CopyLabel *string `json:"copy_label,omitempty"`
}

// Input is what creating an item takes, and what updating one replaces wholesale: a copy_value or
// copy_label left out turns the button off.
type Input struct {
	Label     string
	Value     string
	CopyValue *string
	CopyLabel *string
}
