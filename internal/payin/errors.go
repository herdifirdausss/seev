package payin

import (
	"errors"

	"github.com/herdifirdausss/seev/internal/vendorgw"
)

// ErrUnknownVendor means no verifier is registered for the requested
// vendor name — the caller (webhook receiver, docs/plan/22 Task T3) maps
// this to HTTP 404. Includes "registered but disabled", which never made
// it into the registry in the first place (docs/plan/22 Task T1).
var ErrUnknownVendor = vendorgw.ErrUnknownPayinVendor

// ErrAlreadyPosted is returned by ReplayEvent when the event is already
// posted — replay is for received/failed events only, never a duplicate
// posting attempt on principle even though the ledger's own idempotency
// key would make it harmless (docs/plan/22 Task T4: "posted" -> 409).
var ErrAlreadyPosted = errors.New("payin: event already posted")

// ErrTopupIntentNotFound means no topup intent exists for the given id
// (docs/plan/25 Task T3) — GetTopupIntent maps this to HTTP 404.
var ErrTopupIntentNotFound = errors.New("payin: topup intent not found")

// ErrTopupIntentMismatch means a settling webhook's amount/currency don't
// match the intent it references, or the reference points at an intent
// that isn't 'pending' anymore (already settled or expired) — treated as a
// business failure: redelivery of the exact same webhook will hit the
// exact same mismatch forever, so it must never heal on retry.
var ErrTopupIntentMismatch = errors.New("payin: topup intent mismatch")

// ErrTopupIntentExpired means a settling webhook arrived after the
// intent's expiry window — also a business failure, not retryable.
var ErrTopupIntentExpired = errors.New("payin: topup intent expired")

var ErrNoRoute = errors.New("payin: no route")
