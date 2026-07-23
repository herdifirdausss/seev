package rules

import (
	"context"

	"github.com/herdifirdausss/seev/internal/fraud/model"
)

type Rule interface {
	Name() string
	Screen(context.Context, model.ScreenInput) (model.Verdict, error)
}

// ModeResolver supplies a per-rule override. Implementations may cache and
// fall back to the service's environment default; rules stay independent of
// Postgres and can therefore remain table-testable.
type ModeResolver interface {
	ResolveMode(context.Context, string) (Mode, error)
}
