// Package activities manages club outings: date, place, description and the
// members taking part in them.
package activities

import "time"

type Type string

const (
	TypeCaving   Type = "caving"
	TypeTraining Type = "training"
	TypeOutdoor  Type = "outdoor"
	TypeSocial   Type = "social"
	TypeOther    Type = "other"
)

type Status string

const (
	StatusPlanned   Status = "planned"
	StatusCompleted Status = "completed"
	StatusCancelled Status = "cancelled"
)

type Activity struct {
	ID              int64      `json:"id"`
	Title           string     `json:"title"`
	Type            Type       `json:"type"`
	StartDate       time.Time  `json:"start_date"`
	EndDate         *time.Time `json:"end_date,omitempty"`
	Location        string     `json:"location"`
	Description     *string    `json:"description,omitempty"`
	Difficulty      *int16     `json:"difficulty,omitempty"`
	MaxParticipants *int32     `json:"max_participants,omitempty"`
	Status          Status     `json:"status"`
	LeaderID        *int64     `json:"leader_id,omitempty"`
}

type CreateInput struct {
	Title           string
	Type            Type
	StartDate       time.Time
	EndDate         *time.Time
	Location        string
	Description     *string
	Difficulty      *int16
	MaxParticipants *int32
	LeaderID        *int64
}

type UpdateInput struct {
	Title       *string `json:"title"`
	Description *string `json:"description"`
	Status      *Status `json:"status"`
	Location    *string `json:"location"`
}

type Participant struct {
	ActivityID int64  `json:"activity_id"`
	UserID     int64  `json:"user_id"`
	Role       string `json:"role"`
	Confirmed  bool   `json:"confirmed"`
}
