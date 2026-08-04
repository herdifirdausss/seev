// Package kycvendor defines the provider boundary used by auth-service for
// identity verification. Concrete providers must not know about auth's
// repository or HTTP transport.
package kycvendor

import (
	"context"

	"github.com/google/uuid"
)

const (
	VerdictApprove = "approve"
	VerdictReject  = "reject"
	VerdictRefer   = "refer"
)

// Submission is the provider-neutral input to a KYC verification.
type Submission struct {
	UserID         uuid.UUID
	LevelRequested int
	Payload        map[string]any
}

// Decision is a provider's normalized result.
type Decision struct {
	Verdict string
	Ref     string
	Reason  string
}

// Provider verifies a KYC submission.
type Provider interface {
	Name() string
	Verify(context.Context, Submission) (Decision, error)
}
