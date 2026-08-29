// Package users manages personal data and login credentials. A person can
// exist here without ever getting a login (Credentials), which is why the
// two are kept in separate tables behind a single package.
package users

import "time"

type Role string

const (
	RoleAdmin  Role = "admin"
	RoleBoard  Role = "board"
	RoleMember Role = "member"
)

type User struct {
	ID               int64      `json:"id"`
	FirstName        string     `json:"first_name"`
	LastName         string     `json:"last_name"`
	NationalID       *string    `json:"national_id,omitempty"`
	BirthDate        *time.Time `json:"birth_date,omitempty"`
	Email            string     `json:"email"`
	Phone            *string    `json:"phone,omitempty"`
	Address          *string    `json:"address,omitempty"`
	EmergencyContact *string    `json:"emergency_contact,omitempty"`
	Username         *string    `json:"username,omitempty"`
	Role             *string    `json:"role,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
}

type CreateInput struct {
	FirstName        string     `json:"first_name"`
	LastName         string     `json:"last_name"`
	NationalID       *string    `json:"national_id"`
	BirthDate        *time.Time `json:"birth_date"`
	Email            string     `json:"email"`
	Phone            *string    `json:"phone"`
	Address          *string    `json:"address"`
	EmergencyContact *string    `json:"emergency_contact"`
	Username         string     `json:"username"`
	Password         string     `json:"password"`
	Role             Role       `json:"role"`
}

type UpdateInput struct {
	FirstName        *string `json:"first_name"`
	LastName         *string `json:"last_name"`
	Phone            *string `json:"phone"`
	Address          *string `json:"address"`
	EmergencyContact *string `json:"emergency_contact"`
}

type Credentials struct {
	UserID       int64
	Username     string
	PasswordHash string
	Role         Role
	IsActive     bool
}
