// Package command owns the single execution boundary for money movement.
// Transport, schedulers, recovery workers, and administrative workflows build
// an ExecutionContext and call Executor; they do not call the low-level ledger
// posting service directly.
package command

import (
	"maps"
	"time"

	"github.com/google/uuid"
)

// ExecutionContext is the immutable authorization and audit context for one
// attempt to execute a money-moving command. Claims are copied into the
// context at the boundary; the executor never trusts caller-supplied metadata
// as a substitute for this structure.
type ExecutionContext struct {
	ActorID             uuid.UUID
	TenantID            uuid.UUID
	Source              string
	CorrelationID       string
	AuthorizationClaims map[string]string
	EffectiveTime       time.Time
	RequestOrigin       string
	Currency            string
}

func (c ExecutionContext) normalized(commandUserID uuid.UUID) ExecutionContext {
	if c.AuthorizationClaims != nil {
		claims := make(map[string]string, len(c.AuthorizationClaims))
		maps.Copy(claims, c.AuthorizationClaims)
		c.AuthorizationClaims = claims
	}
	if c.ActorID == uuid.Nil {
		c.ActorID = commandUserID
	}
	if c.Source == "" {
		// A missing source is not proof that the caller is a trusted worker.
		// Handle supplies internal-service explicitly; direct Execute callers
		// must identify themselves or be subject to the end-user gate.
		c.Source = "unknown"
	}
	if c.CorrelationID == "" {
		c.CorrelationID = uuid.NewString()
	}
	if c.EffectiveTime.IsZero() {
		c.EffectiveTime = time.Now().UTC()
	} else {
		c.EffectiveTime = c.EffectiveTime.UTC()
	}
	if c.RequestOrigin == "" {
		c.RequestOrigin = c.Source
	}
	return c
}
