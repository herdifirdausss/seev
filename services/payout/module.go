// Package payout is the stable public facade for the Payout bounded context.
// Business decisions live in internal/payout; persistence, RPC, workers, and HTTP
// concerns stay in dedicated packages.
package payout

import (
	"log/slog"
	"net/http"

	"github.com/redis/go-redis/v9"

	"github.com/herdifirdausss/seev/contracts/clients/fraud"
	vendorgw "github.com/herdifirdausss/seev/contracts/vendorgw"
	"github.com/herdifirdausss/seev/internal/platform/database"
	"github.com/herdifirdausss/seev/internal/platform/security/crypto"
	payoutinternal "github.com/herdifirdausss/seev/services/payout/internal/payout"
	payouthandler "github.com/herdifirdausss/seev/services/payout/internal/transport/http"
)

var (
	ErrUnknownVendor                  = payoutinternal.ErrUnknownVendor
	ErrInvalidTransition              = payoutinternal.ErrInvalidTransition
	ErrInvalidAmount                  = payoutinternal.ErrInvalidAmount
	ErrCurrencyRouteUnavailable       = payoutinternal.ErrCurrencyRouteUnavailable
	ErrNoRoute                        = payoutinternal.ErrNoRoute
	ErrNoVendorAvailable              = payoutinternal.ErrNoVendorAvailable
	ErrScreeningBlocked               = payoutinternal.ErrScreeningBlocked
	ErrScreeningDependencyUnavailable = payoutinternal.ErrScreeningDependencyUnavailable
	ErrSandboxVendorUnavailable       = payoutinternal.ErrSandboxVendorUnavailable
	ErrIntakePaused                   = payoutinternal.ErrIntakePaused
	ErrIntakeRevisionMismatch         = payoutinternal.ErrIntakeRevisionMismatch
)

const (
	VendorCallbackFinalized         = payoutinternal.VendorCallbackFinalized
	VendorCallbackAlreadyFinalized  = payoutinternal.VendorCallbackAlreadyFinalized
	VendorCallbackIgnored           = payoutinternal.VendorCallbackIgnored
	VendorCallbackRecordedUnmatched = payoutinternal.VendorCallbackRecordedUnmatched
)

type (
	IntakeCommandResult = payoutinternal.IntakeCommandResult
	IntakeControl       = payoutinternal.IntakeControl
	Module              struct{ *payoutinternal.Module }
	PayoutRequest       = payoutinternal.PayoutRequest
	Poster              = payoutinternal.Poster
)

func NewModule(db database.DatabaseSQL, poster Poster, registry *vendorgw.Registry, redisClient *redis.Client, logger *slog.Logger, fraudClient *fraudcheck.Client, breaker vendorgw.Breaker, ring *cryptox.Ring) *Module {
	return &Module{Module: payoutinternal.NewModule(db, poster, registry, redisClient, logger, fraudClient, breaker, ring)}
}

func (m *Module) CreateHandler() http.HandlerFunc {
	return payouthandler.New(m.Module).CreateHandler()
}

func (m *Module) GetHandler() http.HandlerFunc {
	return payouthandler.New(m.Module).GetHandler()
}

func (m *Module) AdminRouter() http.Handler {
	return payouthandler.New(m.Module).AdminRouter()
}

func (m *Module) PrivacyRouter() http.Handler {
	return payouthandler.New(m.Module).PrivacyRouter()
}
