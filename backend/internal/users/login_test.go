package users

import "testing"

func TestSlugify(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"already clean", "maria", "maria"},
		{"accents stripped", "María Ángeles Núñez", "maria-angeles-nunez"},
		{"ñ and ç", "Begoña Suárez", "begona-suarez"},
		{"non-alnum collapsed to one hyphen", "O'Donnell  Smith!!", "o-donnell-smith"},
		{"no leading or trailing hyphen", "-Ana-", "ana"},
		{"empty", "", ""},
		{"only punctuation", "'''", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := slugify(c.in); got != c.want {
				t.Errorf("slugify(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestCandidateLogin(t *testing.T) {
	cases := []struct {
		name                string
		firstName, lastName string
		want                string
	}{
		{"both names", "Juan", "Pérez", "juan.perez"},
		{"empty first name", "", "Pérez", "perez"},
		{"empty last name", "Juan", "", "juan"},
		{"both empty", "", "", "socio"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := candidateLogin(c.firstName, c.lastName); got != c.want {
				t.Errorf("candidateLogin(%q, %q) = %q, want %q", c.firstName, c.lastName, got, c.want)
			}
		})
	}
}

func TestLoginWithSuffix(t *testing.T) {
	if got := loginWithSuffix("juan.perez", 2); got != "juan.perez2" {
		t.Errorf("loginWithSuffix(%q, 2) = %q, want %q", "juan.perez", got, "juan.perez2")
	}
}
