package users_test

import (
	"testing"
	"time"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/users"
)

func TestForMemberListingComputesAgeCategoryFromBirthDate(t *testing.T) {
	thisYear := time.Now().Year()
	birthDate := func(age int) time.Time {
		return time.Date(thisYear-age, time.January, 1, 0, 0, 0, 0, time.UTC)
	}

	cases := []struct {
		name      string
		birthDate time.Time
		want      users.AgeCategory
	}{
		{"turns 9 this year (alevín)", birthDate(9), users.AgeCategoryYouth},
		{"turns 10 this year (infantil)", birthDate(10), users.AgeCategoryChild},
		{"turns 13 this year (infantil)", birthDate(13), users.AgeCategoryChild},
		{"turns 14 this year (juvenil)", birthDate(14), users.AgeCategoryJunior},
		{"turns 17 this year (juvenil)", birthDate(17), users.AgeCategoryJunior},
		{"turns 18 this year (mayor)", birthDate(18), users.AgeCategorySenior},
		{"turns 44 this year (mayor)", birthDate(44), users.AgeCategorySenior},
		{"turns 45 this year (veterano A)", birthDate(45), users.AgeCategoryVeteranA},
		{"turns 54 this year (veterano A)", birthDate(54), users.AgeCategoryVeteranA},
		{"turns 55 this year (veterano B)", birthDate(55), users.AgeCategoryVeteranB},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u := users.User{BirthDate: tc.birthDate}
			got := u.ForMemberListing().AgeCategory
			if got != tc.want {
				t.Errorf("expected age_category %v, got %v", tc.want, got)
			}
		})
	}
}
