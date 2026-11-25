package places

import (
	"errors"
	"time"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/access"
)

// Audience is who a level of a place is open to: the club's members alone, or anybody.
type Audience string

const (
	AudienceClub   Audience = "club"
	AudiencePublic Audience = "public"
)

// Valid says whether it is one of the two.
func (a Audience) Valid() bool { return a == AudienceClub || a == AudiencePublic }

// Visibility is how open a place is, in three levels (see migration 00082). The default of every place
// is the club alone at every level.
type Visibility struct {
	// Place is the place itself: that it exists, its name, type, state, zone and measures. Where it is
	// closed, who may not see it does not know it is there.
	Place Audience `json:"place"`
	// Location is the main coordinate, the other points and how to get there.
	Location Audience `json:"location"`
	// Content is the description, the technical sheet and the files.
	Content Audience `json:"content"`
	// Protected locks the place closed (bats, archaeology, private land...): nothing of it may be public.
	Protected bool `json:"protected"`
}

var (
	// ErrInvalidVisibility is an audience that is neither club nor public.
	ErrInvalidVisibility = errors.New("each level of visibility must be club or public")
	// ErrVisibilityTooOpen is a level more open than the place: whoever may not see the place may not
	// see what it holds either.
	ErrVisibilityTooOpen = errors.New("the location and the content cannot be more open than the place")
	// ErrPlaceProtected is something public on a place that is protected.
	ErrPlaceProtected = errors.New("a protected place cannot have anything public")
)

// Validate checks what the database checks too, so that the answer says which rule it breaks.
func (v Visibility) Validate() error {
	if !v.Place.Valid() || !v.Location.Valid() || !v.Content.Valid() {
		return ErrInvalidVisibility
	}
	if v.Protected && (v.Place == AudiencePublic || v.Location == AudiencePublic || v.Content == AudiencePublic) {
		return ErrPlaceProtected
	}
	if v.Place == AudienceClub && (v.Location == AudiencePublic || v.Content == AudiencePublic) {
		return ErrVisibilityTooOpen
	}
	return nil
}

// audience is the audience of the access package that an audience of a place means. A value that is
// not one of the two means nobody, so that what is unknown is closed.
func (a Audience) audience() access.Audience {
	if a == AudiencePublic {
		return access.Public()
	}
	return access.Group(access.ClubGroup)
}

// Rules are the rules of the place: each level is seen by its audience and by the club (who is in the
// group of the club may see everything and is who may change it); the authors and the activities are for
// the club alone, whatever else is said. Nobody but the club writes anything.
func (v Visibility) Rules() access.Rules {
	club := access.Group(access.ClubGroup)
	var rules access.Rules
	for _, part := range access.Parts {
		audience := club
		switch part {
		case access.PartPlace, access.PartMeasures:
			audience = v.Place.audience()
		case access.PartCoordinates, access.PartPoints, access.PartAccess:
			audience = v.Location.audience()
		case access.PartSheet, access.PartFiles:
			audience = v.Content.audience()
		}
		rules = append(rules,
			access.Rule{Part: part, Action: access.ActionSee, Audience: audience},
			access.Rule{Part: part, Action: access.ActionWrite, Audience: club})
		if audience != club {
			// The club sees what anybody sees.
			rules = append(rules, access.Rule{Part: part, Action: access.ActionSee, Audience: club})
		}
	}
	return rules
}

// CanSee says whether the viewer may see the part of a place with this visibility, at `now`.
func (v Visibility) CanSee(viewer access.Viewer, now time.Time, part access.Part) bool {
	return v.Rules().Allows(viewer, now, access.ActionSee, part)
}
