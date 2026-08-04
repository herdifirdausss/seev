package ledger

import "github.com/google/uuid"

func stringPtr(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func uuidPtr(value uuid.UUID) *uuid.UUID {
	if value == uuid.Nil {
		return nil
	}
	return &value
}
