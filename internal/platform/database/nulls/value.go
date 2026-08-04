// Package nulls contains explicit SQL-value conversions for optional IDs and
// strings. It is infrastructure-only; ownership decisions remain in the
// calling service repository.
package nulls

import "github.com/google/uuid"

// String returns nil for an empty string so SQL stores NULL rather than an
// empty value.
func String(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// UUID returns nil for uuid.Nil so SQL stores NULL rather than an all-zero ID.
func UUID(value uuid.UUID) any {
	if value == uuid.Nil {
		return nil
	}
	return value
}
