package osmmirror

import (
	"testing"
	"unicode/utf8"
)

// translate() in SQL needs as many characters to go to as to take from.
func TestAccentTablesHaveTheSameLength(t *testing.T) {
	if utf8.RuneCountInString(accentFrom) != utf8.RuneCountInString(accentTo) {
		t.Errorf("%d characters to take and %d to give", utf8.RuneCountInString(accentFrom), utf8.RuneCountInString(accentTo))
	}
}
