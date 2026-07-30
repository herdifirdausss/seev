// Package client holds internal/merchant's typed Gateway-side clients to
// PayinService and PayoutService (docs/roadmap/archive/57-c1-merchant-b2b-api.md
// §3.1's package boundary) — Gateway calls these owner services over gRPC,
// never in-process, since PayinService/PayoutService remain independent
// processes/binaries that own their own lifecycle state (§3.3).
package client

import "errors"

// The three sentinel errors below are the ENTIRE vocabulary this package
// translates gRPC status codes into. internal/merchant/api maps each one
// to its own §6.7 stable HTTP error code — this package intentionally
// stays ignorant of HTTP concerns (that mapping belongs to the api
// package, not here).
var (
	// ErrNotFound means the owner service reported the resource does not
	// exist (or, for a merchant-scoped read, belongs to a different
	// tenant — the owner service's own tenant check already collapsed
	// that distinction before this package ever sees it).
	ErrNotFound = errors.New("merchant/client: resource not found")

	// ErrValidation means the owner service rejected the request shape
	// itself (codes.InvalidArgument) — a Gateway-side bug (bad DTO
	// mapping), never a condition the merchant caller can fix by retrying
	// the exact same request.
	ErrValidation = errors.New("merchant/client: invalid request")

	// ErrOwnerUnavailable covers every other owner-service failure mode
	// this package currently knows about: no routing rule/vendor
	// available, a sandbox tenant's mock vendor not being registered, the
	// operator having paused intake, or a genuine owner-service internal
	// error. All of these read as "the owner service cannot currently
	// service this request" from the B2B edge's perspective — none of
	// them are the merchant caller's fault, and §6.7's stable code list
	// has exactly one bucket for that: 503 OWNER_SERVICE_UNAVAILABLE.
	ErrOwnerUnavailable = errors.New("merchant/client: owner service unavailable")
)
