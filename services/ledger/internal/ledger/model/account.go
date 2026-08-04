package model

import "github.com/google/uuid"

// Account is the public DTO for an account, returned by provisioning and
// read APIs. It intentionally excludes internal-only fields.
type Account struct {
	ID         uuid.UUID
	OwnerID    uuid.UUID
	Type       string
	Currency   string
	PocketCode string
	Status     string
}
