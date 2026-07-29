package auth

import (
	"context"
	"net/http"
	"sync/atomic"

	"github.com/herdifirdausss/seev/internal/merchant/repository"
	"github.com/herdifirdausss/seev/pkg/middleware"
	"github.com/herdifirdausss/seev/pkg/response"
)

// SettingKeyB2BAPIEnabled is the merchant_settings row this flag reads
// and writes — Plan 57 T9's "global route-disable control": an operator
// incident-response kill switch for the ENTIRE merchant B2B API surface,
// independent of any single tenant's own suspension (RequireMerchantAuth's
// own tenant.Status check, above).
const SettingKeyB2BAPIEnabled = "b2b_api_enabled"

// GlobalFlag caches merchant_settings' b2b_api_enabled row in memory so
// RequireB2BEnabled never adds a database round trip to the hot request
// path — Refresh (called periodically by
// internal/merchant.Module.StartObservabilityRefresher, same cadence as
// the T9 gauges) is the only thing that ever hits the database. A row
// that has NEVER been written reads as enabled=true (the default,
// pre-incident state) — only an explicit SetEnabled(false) call disables
// traffic; there is no way to reach "disabled" by omission.
type GlobalFlag struct {
	repo    repository.SettingsRepository
	enabled atomic.Bool
}

// NewGlobalFlag panics on a nil repository — matches this package's own
// construct-now convention. Starts enabled=true; the first Refresh call
// (or an explicit prior SetEnabled) is what can ever change that.
func NewGlobalFlag(repo repository.SettingsRepository) *GlobalFlag {
	if repo == nil {
		panic("merchant/auth: NewGlobalFlag requires a non-nil SettingsRepository")
	}
	f := &GlobalFlag{repo: repo}
	f.enabled.Store(true)
	return f
}

// Enabled reports the last-refreshed state — safe for concurrent use from
// every request goroutine.
func (f *GlobalFlag) Enabled() bool { return f.enabled.Load() }

// Refresh reloads the in-memory value from merchant_settings. Called
// periodically, never per-request (see the type's own doc comment). A
// query error leaves the previous in-memory value untouched — a
// transient DB blip must never accidentally flip live traffic off.
func (f *GlobalFlag) Refresh(ctx context.Context) error {
	value, found, err := f.repo.Get(ctx, SettingKeyB2BAPIEnabled)
	if err != nil {
		return err
	}
	if !found {
		f.enabled.Store(true)
		return nil
	}
	f.enabled.Store(value == "true")
	return nil
}

// SetEnabled persists the new value AND updates the in-memory cache
// immediately (not waiting for the next periodic Refresh) — an operator
// flipping this switch during an incident needs it to take effect on the
// very next request, on every process that has already loaded this flag
// via the same underlying database.
func (f *GlobalFlag) SetEnabled(ctx context.Context, enabled bool, actor string) error {
	value := "false"
	if enabled {
		value = "true"
	}
	if err := f.repo.Set(ctx, SettingKeyB2BAPIEnabled, value, actor); err != nil {
		return err
	}
	f.enabled.Store(enabled)
	return nil
}

// RequireB2BEnabled is the enforcement side — mount BEFORE
// RequireMerchantAuth in any future merchant-facing route chain (T6's own
// B2B HTTP handlers, deferred separately — see docs/roadmap/active/57's
// own T6 Result note) so a global disable short-circuits before an API
// key is even looked up. Returns 503, matching this codebase's own
// "service degraded, not the caller's fault" convention (compare
// response.ServiceUnavailable's other callers).
func RequireB2BEnabled(flag *GlobalFlag) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !flag.Enabled() {
				response.ServiceUnavailable(w, "B2B_API_DISABLED", "the merchant B2B API is temporarily disabled")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
