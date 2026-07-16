package handler

import (
	"context"
	"net/http/httputil"

	payinv1 "github.com/herdifirdausss/seev/gen/payin/v1"
	payoutv1 "github.com/herdifirdausss/seev/gen/payout/v1"
	"github.com/herdifirdausss/seev/internal/notify"
	"github.com/herdifirdausss/seev/pkg/cache"
	"github.com/herdifirdausss/seev/pkg/database"
	"github.com/herdifirdausss/seev/pkg/messaging"
)

// Dependencies groups all handler dependencies as interfaces.
// This allows any field to be replaced with a mock during unit tests.
// Cache may be nil when REDIS_ENABLED=false (docs/plan/12 Task T1) —
// every consumer must nil-check it rather than assume it's always
// populated; see CacheOrNil for safely constructing this field.
type Dependencies struct {
	DB    database.DatabaseSQL
	Cache cache.FullCache
	MQ    messaging.Broker
	// LedgerProxy forwards the user ledger API without sharing ledger code.
	LedgerProxy *httputil.ReverseProxy
	// LedgerReady is the gRPC health probe used by monolith readiness.
	LedgerReady func(context.Context) error
	// Payin handles vendor webhook deliveries (docs/plan/22 Task T2) —
	// nil-checked at both mount sites: the public listener's
	// /webhooks/{vendor} receiver and the internal listener's
	// /admin/payin/ admin surface. Nil (no vendor configured) means every
	// /webhooks/* request 404s, byte-identical to before this feature
	// existed.
	Payin payinv1.PayinServiceClient
	// Payout orchestrates user withdrawals (docs/plan/23 Task T5) —
	// nil-checked at both mount sites: the public listener's
	// POST/GET /api/v1/payout endpoints and the internal listener's
	// /admin/payout/ admin surface. Nil (no vendor configured) means every
	// payout route 404s, byte-identical to before this feature existed.
	Payout payoutv1.PayoutServiceClient
	// Notify serves the in-app notification inbox (docs/plan/25 Task T4) —
	// GET/POST /api/v1/notifications on the public listener. Its consumer
	// goroutine (Start/Stop) is driven directly from cmd/gateway/main.go
	// alongside the other background workers, not through this struct.
	Notify *notify.Module
}

// CacheOrNil returns c wrapped as a cache.FullCache, or a genuinely nil
// interface if c is nil. Assigning a nil *cache.Cache directly to a
// cache.FullCache field produces a NON-nil interface (the classic Go
// typed-nil gotcha) — callers doing `deps.Cache != nil` would then always
// be true even though every method call on it panics. Use this helper at
// every construction site instead of assigning the pointer directly.
func CacheOrNil(c *cache.Cache) cache.FullCache {
	if c == nil {
		return nil
	}
	return c
}
