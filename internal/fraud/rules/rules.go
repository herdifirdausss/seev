package rules

import (
	"context"

	"github.com/herdifirdausss/seev/internal/fraud/model"
)

type Rule interface {
	Name() string
	Screen(context.Context, model.ScreenInput) (model.Verdict, error)
}
