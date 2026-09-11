package users

import (
	"fmt"
	"regexp"
	"strings"
)

// loginFormat matches the shape both an auto-generated login and an
// admin-typed one must have: lowercase ASCII letters and digits, with
// single dots or hyphens allowed between runs of them (never at the
// edges, never doubled) — the same charset a directory name can hold on
// disk with no escaping, and safe to drop straight into a URL path
// segment.
var loginFormat = regexp.MustCompile(`^[a-z0-9]+([.-][a-z0-9]+)*$`)

// diacritics folds the accented Latin letters a name in this club is
// likely to contain down to their unaccented ASCII equivalent, so a
// generated login never needs percent-encoding or quoting to appear in
// a URL or a filesystem path.
var diacritics = strings.NewReplacer(
	"á", "a", "à", "a", "ä", "a", "â", "a", "ã", "a",
	"é", "e", "è", "e", "ë", "e", "ê", "e",
	"í", "i", "ì", "i", "ï", "i", "î", "i",
	"ó", "o", "ò", "o", "ö", "o", "ô", "o", "õ", "o",
	"ú", "u", "ù", "u", "ü", "u", "û", "u",
	"ñ", "n", "ç", "c",
)

var nonLoginChars = regexp.MustCompile(`[^a-z0-9]+`)

// slugify folds s down to the lowercase-ASCII, hyphen-separated shape a
// login segment needs: accents stripped, anything else that isn't a
// letter or digit collapsed to a single hyphen, and no leading or
// trailing hyphen left over. Lowercases *before* stripping accents, not
// after — diacritics only maps the lowercase accented forms ("á", not
// "Á"), so a capitalized one (the common case: names arrive
// title-cased, "Ángeles", "Óscar") would otherwise reach it unmapped,
// get case-folded by ToLower into a lowercase-but-still-accented
// letter ("á"), and then have nonLoginChars below silently drop it as
// if it were punctuation — quietly eating a letter out of the login
// instead of folding it to its unaccented equivalent.
func slugify(s string) string {
	s = diacritics.Replace(strings.ToLower(s))
	s = nonLoginChars.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

// candidateLogin is the login a new person gets if nobody else already
// has it: first and last name slugified and dot-joined. Repository.Create
// appends a numeric suffix instead when this exact value turns out to
// already belong to someone else.
func candidateLogin(firstName, lastName string) string {
	first, last := slugify(firstName), slugify(lastName)
	switch {
	case first == "" && last == "":
		return "socio"
	case first == "":
		return last
	case last == "":
		return first
	default:
		return first + "." + last
	}
}

func loginWithSuffix(base string, n int) string {
	return fmt.Sprintf("%s%d", base, n)
}
