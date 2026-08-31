package users_test

import (
	"testing"
	"time"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/users"
)

func TestForMemberListingComputesAgeCategoryFromBirthDate(t *testing.T) {
	thisYear := time.Now().Year()
	birthDate := func(age int) *time.Time {
		d := time.Date(thisYear-age, time.January, 1, 0, 0, 0, 0, time.UTC)
		return &d
	}

	cases := []struct {
		name      string
		birthDate *time.Time
		want      *users.AgeCategory
	}{
		{"unknown birth date", nil, nil},
		{"turns 13 this year", birthDate(13), ptr(users.AgeCategoryChild)},
		{"turns 14 this year", birthDate(14), ptr(users.AgeCategoryTeen)},
		{"turns 17 this year", birthDate(17), ptr(users.AgeCategoryTeen)},
		{"turns 18 this year", birthDate(18), ptr(users.AgeCategoryAdult)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u := users.User{BirthDate: tc.birthDate}
			got := u.ForMemberListing().AgeCategory
			if tc.want == nil {
				if got != nil {
					t.Errorf("expected nil age_category, got %v", *got)
				}
				return
			}
			if got == nil || *got != *tc.want {
				t.Errorf("expected age_category %v, got %v", *tc.want, got)
			}
		})
	}
}

func ptr[T any](v T) *T { return &v }
