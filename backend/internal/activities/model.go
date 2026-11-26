// Package activities manages club outings: start/end date, description
// and the members taking part in them.
package activities

import (
	"time"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/placelinks"
)

type Type string

const (
	TypeCaving    Type = "caving"
	TypeCanyoning Type = "canyoning"
	TypeTraining  Type = "training"
	TypeSocial    Type = "social"
	TypeOther     Type = "other"
)

// Valid reports whether t is one of the activity_type enum's values.
// Checked in Go (Service.Create/Update) so a misspelled one is a 400 rather
// than the 500 Postgres' own enum error would become; Visibility.Valid below does the
// same for its.
func (t Type) Valid() bool {
	switch t {
	case TypeCaving, TypeCanyoning, TypeTraining, TypeSocial, TypeOther:
		return true
	}
	return false
}

type Visibility string

const (
	VisibilityPublic  Visibility = "public"
	VisibilityPrivate Visibility = "private"
)

func (v Visibility) Valid() bool {
	return v == VisibilityPublic || v == VisibilityPrivate
}

type Activity struct {
	ID          int64     `json:"id"`
	Title       string    `json:"title"`
	Type        Type      `json:"type"`
	StartDate   time.Time `json:"start_date"`
	EndDate     time.Time `json:"end_date"`
	Description *string   `json:"description,omitempty"`
	// Cancelled is the only state that is stored. Whether the activity is still to come, going on
	// or over follows from its dates, so nothing has to be kept right by hand (migration
	// 00042_activities_cancelled.sql).
	Cancelled bool `json:"cancelled"`
	// LeaderID/LeaderLogin are the organizer: always set, defaulting to
	// whoever creates the activity — admin/board can instead name
	// someone else at creation time, or reassign it afterward (see
	// Handler.create/Handler.update; a plain member can't do either).
	// The leader is the only person besides admin/board who can edit or
	// cancel their own activity.
	LeaderID    int64  `json:"-"`
	LeaderLogin string `json:"leader_login"`
	// Visibility gates who may sign people up: in a public activity,
	// anybody can sign up anybody (themselves included); in a private
	// one, only the organizer, admin, board or somebody who is already
	// signed up — see Handler.join.
	Visibility Visibility `json:"visibility"`
	// MaxParticipants is an optional limit on how many people go: the members signed up and the
	// guests together. Nobody can be added past it (ErrActivityFull).
	MaxParticipants *int `json:"max_participants,omitempty"`
}

// PlaceRef is a place an activity goes to, as the activity says it: enough to name it and link to it.
// The activities keep only the ids of the places; the catalogue names them (PlaceLookup).
type PlaceRef = placelinks.PlaceRef

// PlaceVisit is how often a person went to a place: in how many activities, started and not cancelled, they
// organized or took part in that went there, and the end date of the latest of them.
type PlaceVisit struct {
	PlaceID   int64
	Visits    int
	LastVisit time.Time
}

// VisitedPlace is a place a person has been to, with how often (GET /users/{login}/places): what the
// catalogue says of the place (its name, what it is and where) and how many times they went.
type VisitedPlace struct {
	ID int64 `json:"id"`
	// TypeName is what it is: the subtype of a cavity ("Sima"), else its type.
	TypeName string `json:"type_name"`
	Name     string `json:"name"`
	Where    string `json:"where"`
	// Visits is in how many activities, started and not cancelled, the person took part and that went to the
	// place, and LastVisit the end date of the latest of them.
	Visits    int       `json:"visits"`
	LastVisit time.Time `json:"last_visit"`
}

// MaxPlaces is the most places an activity can be linked to: far more than any outing, only to keep a
// mistake from linking hundreds.
const MaxPlaces = 30

type CreateInput struct {
	Title       string
	Type        Type
	StartDate   time.Time
	EndDate     time.Time
	Description *string
	LeaderID    int64
	Visibility  Visibility
	// MaxParticipants limits those who can go, if given (at least 1).
	MaxParticipants *int
	// PlaceIDs are the places the activity goes to, linked in the same operation.
	PlaceIDs []int64
	// ParticipantUserIDs are signed up in the same operation as the
	// activity itself, since whoever creates it already knows who's going.
	ParticipantUserIDs []int64
	// CreatedBy is who creates the activity, and so who signs those participants up.
	CreatedBy int64
}

type UpdateInput struct {
	Title       *string
	Type        *Type
	StartDate   *time.Time
	EndDate     *time.Time
	Description *string
	Cancelled   *bool
	Visibility  *Visibility
	// SetMaxParticipants says MaxParticipants is being changed: to its value, or, if that is nil, to
	// no limit. It cannot go below the people already going (ErrMaxBelowAttendance).
	SetMaxParticipants bool
	MaxParticipants    *int
	// LeaderID reassigns the organizer — admin/board only, see
	// Handler.update.
	LeaderID *int64
}

type Participant struct {
	ActivityID int64  `json:"activity_id"`
	UserID     int64  `json:"-"`
	UserLogin  string `json:"user_login"`
	// Federated says whether the person has a federative license for the current year. It is only
	// information: any member can join any activity, federated or not.
	Federated bool `json:"federated"`
	// AddedByLogin is who signed the person up (themselves, or somebody else) and AddedAt when. For
	// those signed up before it was recorded it is the organizer, and the activity's creation.
	AddedByLogin string    `json:"added_by_login"`
	AddedAt      time.Time `json:"added_at"`
}

// Holiday is a club holiday — the calendar view (see GET /holidays)
// marks these alongside weekends (computed from the date itself, no
// table needed for those). admin/board can add or remove one (see
// Handler.createHoliday/deleteHoliday); there's no update, since
// correcting one is just deleting and re-adding it.
type Holiday struct {
	ID   int64     `json:"id"`
	Date time.Time `json:"date"`
	Name string    `json:"name"`
}
