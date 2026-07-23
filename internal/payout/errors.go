package payout

import "errors"

// ErrUnknownVendor means no payout provider is registered for the
// requested vendor name (docs/roadmap/archive/23 Task T3) — mirrors
// internal/payin.ErrUnknownVendor.
var ErrUnknownVendor = errors.New("payout: unknown vendor")

// ErrInvalidTransition means the requested operation doesn't apply to the
// request's current status (e.g. cancelling an already-settled request).
var ErrInvalidTransition = errors.New("payout: invalid transition for current status")

var ErrNoRoute = errors.New("payout: no route")

// ErrNoVendorAvailable means at least one routing rule matched, but every
// candidate vendor was either unregistered or its circuit breaker is open
// (docs/roadmap/archive/40 Task T2) — distinct from ErrNoRoute (no rule matched at
// all). The gateway handler maps this to 503 VENDOR_UNAVAILABLE.
var ErrNoVendorAvailable = errors.New("payout: no vendor available")

// ErrScreeningBlocked means fraud screening rejected the payout BEFORE any
// payout_requests row was inserted or any money was held (docs/roadmap/archive/37 Task
// T5) — wrapped with the verdict's reason via fmt.Errorf("%w: %s", ...).
// The audit trail for a blocked attempt lives only in fraud-service's own
// screening_events, since payout never persists anything for it.
var ErrScreeningBlocked = errors.New("payout: screening blocked")

// ErrScreeningDependencyUnavailable means fraud-service is reachable but
// explicitly reported its velocity dependency (Redis) is down
// (docs/roadmap/archive/45 Task T3/K4) — like ErrScreeningBlocked, no payout_requests
// row is ever inserted and no hold is posted; unlike a generic screening
// infra error (which fails open), this one fails CLOSED. The gateway
// handler maps this to 503 DEPENDENCY_UNAVAILABLE.
var ErrScreeningDependencyUnavailable = errors.New("payout: screening dependency unavailable")
