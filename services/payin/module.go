// Package payin is the stable public facade for the Payin bounded context.
// Business decisions live in internal/payin; persistence, RPC, workers, and HTTP
// concerns stay in dedicated packages.
package payin

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/herdifirdausss/seev/contracts/clients/fraud"
	vendorgw "github.com/herdifirdausss/seev/contracts/vendorgw"
	"github.com/herdifirdausss/seev/internal/platform/database"
	"github.com/herdifirdausss/seev/internal/platform/security/crypto"
	payininternal "github.com/herdifirdausss/seev/services/payin/internal/payin"
	payinhandler "github.com/herdifirdausss/seev/services/payin/internal/transport/http"
)

var (
	ErrAlreadyPosted                  = payininternal.ErrAlreadyPosted
	ErrTopupIntentNotFound            = payininternal.ErrTopupIntentNotFound
	ErrTopupIntentMismatch            = payininternal.ErrTopupIntentMismatch
	ErrTopupIntentExpired             = payininternal.ErrTopupIntentExpired
	ErrInvalidAmount                  = payininternal.ErrInvalidAmount
	ErrCurrencyRouteUnavailable       = payininternal.ErrCurrencyRouteUnavailable
	ErrFeeQuoteRequired               = payininternal.ErrFeeQuoteRequired
	ErrNoRoute                        = payininternal.ErrNoRoute
	ErrNoVendorAvailable              = payininternal.ErrNoVendorAvailable
	ErrScreeningDependencyUnavailable = payininternal.ErrScreeningDependencyUnavailable
	ErrSandboxVendorUnavailable       = payininternal.ErrSandboxVendorUnavailable
	ErrIntakePaused                   = payininternal.ErrIntakePaused
	ErrIntakeRevisionMismatch         = payininternal.ErrIntakeRevisionMismatch
)

const (
	VendorCallbackFinalized         = payininternal.VendorCallbackFinalized
	VendorCallbackAlreadyFinalized  = payininternal.VendorCallbackAlreadyFinalized
	VendorCallbackIgnored           = payininternal.VendorCallbackIgnored
	VendorCallbackRecordedUnmatched = payininternal.VendorCallbackRecordedUnmatched
)

type (
	IntakeCommandResult = payininternal.IntakeCommandResult
	IntakeControl       = payininternal.IntakeControl
	Module              struct{ *payininternal.Module }
	Poster              = payininternal.Poster
	TopupIntent         = payininternal.TopupIntent
	WebhookEvent        = payininternal.WebhookEvent
)

func NewModule(db database.DatabaseSQL, poster Poster, registry *vendorgw.Registry, topupTTL time.Duration, logger *slog.Logger, fraudClient *fraudcheck.Client, breaker vendorgw.Breaker, ring *cryptox.Ring) *Module {
	return &Module{Module: payininternal.NewModule(db, poster, registry, topupTTL, logger, fraudClient, breaker, ring)}
}

func IsBusinessFailure(err error) bool { return payininternal.IsBusinessFailure(err) }

func (m *Module) AdminRouter() http.Handler {
	return payinhandler.New(m.Module).AdminRouter()
}

func (m *Module) PrivacyRouter() http.Handler {
	return payinhandler.New(m.Module).PrivacyRouter()
}
