// Package payin is the public facade for the payin module (docs/plan/22
// Task T2, decision K-T2) — verifies vendor webhook deliveries, dedups
// them, and posts them as money_in to the ledger. This is the ONLY package
// other code may import from internal/payin — importing
// internal/payin/repository or internal/payin/model directly from outside
// this module is a boundary violation (docs/plan/01-target-architecture.md,
// enforced by boundary_test.go).
package payin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"

	payinv1 "github.com/herdifirdausss/seev/gen/payin/v1"
	"github.com/herdifirdausss/seev/internal/payin/grpcserver"
	"github.com/herdifirdausss/seev/internal/payin/model"
	"github.com/herdifirdausss/seev/internal/payin/repository"
	"github.com/herdifirdausss/seev/internal/vendorgw"
	"github.com/herdifirdausss/seev/pkg/database"
	"github.com/herdifirdausss/seev/pkg/fraudcheck"
	"github.com/herdifirdausss/seev/pkg/generalutil"
	"github.com/herdifirdausss/seev/pkg/ledgerclient"
	"github.com/herdifirdausss/seev/pkg/ledgererr"
	"github.com/herdifirdausss/seev/pkg/middleware"
)

// Re-exported types so callers never need to import internal/payin/model.
type WebhookEvent = model.WebhookEvent

type WebhookOutcome = string

const (
	WebhookOutcomeOK              WebhookOutcome = "ok"
	WebhookOutcomeIgnored         WebhookOutcome = "ignored"
	WebhookOutcomeBusinessFailure WebhookOutcome = "business_failure"
)

// Poster is the subset of ledger.Module's behavior payin needs — a local
// structural interface rather than a dependency on the concrete
// *ledger.Module type, so unit tests can inject a mock without touching
// Postgres, and so a future HTTP-client shim (docs/plan/24, extraction)
// satisfies this same interface with zero payin-side code changes.
type Poster interface {
	Post(ctx context.Context, cmd ledgerclient.Command) error
	// GetUserCurrency resolves the currency CreateTopupIntent records on a
	// new payin_topup_intents row (docs/plan/25 Task T3).
	GetUserCurrency(ctx context.Context, userID uuid.UUID, pocketCode string) (string, error)
}

// RegisterGRPC exposes the internal payin service contract.
func (m *Module) RegisterGRPC(server *grpc.Server) {
	payinv1.RegisterPayinServiceServer(server, grpcserver.New(m, ErrTopupIntentNotFound, ErrNoRoute, ErrNoVendorAvailable, ErrScreeningDependencyUnavailable))
}

// Module is the public facade for the payin module.
type Module struct {
	repo     repository.Repository
	routing  repository.RoutingRepository
	poster   Poster
	registry *vendorgw.Registry
	logger   *slog.Logger
	// topupTTL is how long a topup intent stays 'pending' before being
	// treated as expired (docs/plan/25 Task T3). <=0 falls back to 24h.
	topupTTL time.Duration
	// fraudClient screens deposits before posting (docs/plan/37 Task T4).
	// nil is a valid, fully-supported configuration — no screening runs.
	fraudClient *fraudcheck.Client
	// breaker tracks per-vendor circuit health (docs/plan/40 Task T1) — nil
	// is a valid, fully-supported configuration (byte-identical to before
	// this feature existed: every registered vendor is always "allowed").
	breaker vendorgw.Breaker
}

// NewModule wires the payin module. Vendor and gateway selection comes
// from the routing repository; topupTTL <=0 defaults to 24h. fraudClient
// may be nil to disable pre-posting fraud screening entirely.
func NewModule(db database.DatabaseSQL, poster Poster, registry *vendorgw.Registry, topupTTL time.Duration, logger *slog.Logger, fraudClient *fraudcheck.Client, breaker vendorgw.Breaker) *Module {
	if logger == nil {
		logger = slog.Default()
	}
	return &Module{
		repo:        repository.NewRepository(db),
		routing:     repository.NewRoutingRepository(db),
		poster:      poster,
		registry:    registry,
		logger:      logger,
		topupTTL:    topupTTL,
		fraudClient: fraudClient,
		breaker:     breaker,
	}
}

// HandleWebhook processes one webhook delivery end to end (docs/plan/22
// Task T2 step 3): verify -> dedup -> post to ledger -> record outcome.
//
// Return value contract for the HTTP layer (docs/plan/22 Task T3):
//   - ErrUnknownVendor: no verifier registered for this vendor -> 404.
//   - errors.Is(err, vendorgw.ErrInvalidSignature): bad signature,
//     NOTHING was written -> 401.
//   - nil: acknowledged (event ignored, already posted, or freshly
//     posted) -> 200.
//   - errors.As(err, *ledgererr.LedgerError): business failure (e.g. account
//     suspended) — WON'T heal on redelivery, but the event is durably
//     recorded 'failed' for admin replay -> caller still returns 200 so
//     the vendor stops retrying (docs/plan/22 Task T2 step 3.5 note).
//   - any other error: infra failure, event left 'received' -> 503 so the
//     vendor redelivers.
func (m *Module) HandleWebhook(ctx context.Context, vendor string, headers http.Header, rawBody []byte) error {
	_, err := m.HandleWebhookResult(ctx, vendor, headers, rawBody)
	return err
}

// HandleWebhookResult preserves the outcome distinction needed by the gRPC
// boundary while HandleWebhook retains the legacy error-only HTTP contract.
func (m *Module) HandleWebhookResult(ctx context.Context, vendor string, headers http.Header, rawBody []byte) (WebhookOutcome, error) {
	verifier, ok := m.registry.Payin(vendor)
	if !ok {
		return "", ErrUnknownVendor
	}

	ev, err := verifier.VerifyAndParse(headers, rawBody)
	if err != nil {
		return "", err // includes vendorgw.ErrInvalidSignature — no side effect
	}
	if ev == nil {
		return WebhookOutcomeIgnored, nil
	}

	mapping, found, mapErr := m.routing.GetVendorGateway(ctx, vendor)
	if mapErr != nil {
		return "", mapErr
	}
	if !found {
		// A registered verifier with no gateway mapping is a config bug
		// (docs/plan/22 Task T2 step 4 requires every registered vendor to
		// have one) — fail loudly rather than posting with a gateway that
		// isn't in constant.ValidGateways and would fail downstream anyway
		// with a much more confusing error.
		return "", fmt.Errorf("payin: vendor %q has no gateway mapping configured", vendor)
	}
	gateway := mapping.Gateway

	// Topup intent resolution (docs/plan/25 Task T3): the reference travels
	// in the existing ExternalRef field, so a vendor never needs to learn
	// the internal user_id. Resolved BEFORE persisting the event so the
	// stored row itself carries the correct user_id either way. No intent
	// found for this reference (or ExternalRef is empty) falls back to the
	// webhook payload's own user_id — backward compatible with every
	// pre-existing payin flow/test.
	userID := ev.UserID
	var intentErr error
	if ev.ExternalRef != "" {
		intent, found, lookupErr := m.repo.GetTopupIntentByReference(ctx, ev.ExternalRef)
		if lookupErr != nil {
			return "", fmt.Errorf("payin: lookup topup intent: %w", lookupErr)
		}
		if found {
			switch {
			case intent.Status != model.TopupStatusPending:
				// Catches BOTH 'settled' (reference already consumed —
				// posting again under intent.UserID would double-credit
				// under a different vendor_event_id, which the ledger's
				// own idempotency key can't catch since it's keyed by
				// vendor_event_id, not by reference) and 'expired'.
				intentErr = fmt.Errorf("%w: intent %s is not pending (status=%s)",
					ErrTopupIntentMismatch, intent.Reference, intent.Status)
			case !intent.ExpiresAt.After(time.Now()):
				intentErr = fmt.Errorf("%w: intent %s expired at %s",
					ErrTopupIntentExpired, intent.Reference, intent.ExpiresAt)
			case !intent.Amount.Equal(ev.Amount) || intent.Currency != ev.Currency:
				intentErr = fmt.Errorf("%w: intent %s expects %s %s, webhook carries %s %s",
					ErrTopupIntentMismatch, intent.Reference, intent.Amount, intent.Currency, ev.Amount, ev.Currency)
			default:
				userID = intent.UserID
			}
		}
	}

	stored, err := m.repo.GetOrInsert(ctx, model.WebhookEvent{
		ID:            generalutil.NewV7(),
		Vendor:        vendor,
		VendorEventID: ev.VendorEventID,
		ExternalRef:   ev.ExternalRef,
		UserID:        userID,
		Amount:        ev.Amount,
		Currency:      ev.Currency,
		Raw:           rawBody,
		RequestID:     middleware.RequestIDFromCtx(ctx),
	})
	if err != nil {
		return "", fmt.Errorf("payin: persist webhook event: %w", err)
	}
	if stored.Status == "posted" {
		return WebhookOutcomeOK, nil
	}

	if intentErr != nil {
		// A business failure discovered before ever attempting to post —
		// never retryable (the exact same mismatch/expiry recurs on every
		// redelivery), so the event is marked 'failed' directly rather
		// than going through postAndFinalize's ledger-error classification.
		if markErr := m.repo.MarkFailed(ctx, stored.ID, intentErr.Error()); markErr != nil {
			m.logger.Error("payin: mark failed failed", slog.Any("error", markErr), slog.String("event_id", stored.ID.String()))
		}
		return WebhookOutcomeBusinessFailure, &businessError{err: intentErr}
	}

	err = m.postAndFinalize(ctx, stored, gateway)
	if IsBusinessFailure(err) {
		return WebhookOutcomeBusinessFailure, err
	}
	if err != nil {
		return "", err
	}
	return WebhookOutcomeOK, nil
}

// postAndFinalize posts stored to the ledger and updates its status
// accordingly. Called both from HandleWebhook (fresh/retried delivery) and
// ReplayEvent (admin-triggered retry) — identical logic either way.
func (m *Module) postAndFinalize(ctx context.Context, ev model.WebhookEvent, gateway string) error {
	if m.fraudClient != nil {
		verdict, ferr := m.fraudClient.Check(ctx, "topup", "money_in", ev.UserID, ev.Amount, ev.Currency)
		if ferr != nil {
			if errors.Is(ferr, fraudcheck.ErrDependencyUnavailable) {
				// docs/plan/45 Task T3/K4: fraud-service is reachable but
				// its velocity dependency is down — fail CLOSED, unlike a
				// generic infra error below (fail open). NOT a
				// businessError: the identical redelivery succeeds once
				// Redis recovers, so the webhook receiver must respond in a
				// way that makes the vendor retry, not give up.
				m.logger.Warn("payin: screening dependency unavailable, failing closed", slog.String("event_id", ev.ID.String()))
				return ErrScreeningDependencyUnavailable
			}
			// Infra failure — fail open: a real deposit already arrived at
			// the vendor, so we don't strand it over a screening outage.
			// The vendor's own idempotent event still flows into fraud's
			// velocity view once the service is back for post-hoc detection.
			m.logger.Error("payin: screening check error, failing open", slog.Any("error", ferr), slog.String("event_id", ev.ID.String()))
		} else if verdict.Block {
			// A definite business decision (fail-closed): won't heal on
			// vendor redelivery, so mark 'blocked' (distinct from 'failed'
			// so an operator can tell fraud rejection apart from a ledger
			// posting failure at a glance) and let the webhook receiver
			// still ack 200 — the vendor already has the money, retrying
			// changes nothing; recovery is an admin replay, which itself
			// re-screens via this same code path.
			if markErr := m.repo.MarkBlocked(ctx, ev.ID, verdict.Reason); markErr != nil {
				m.logger.Error("payin: mark blocked failed", slog.Any("error", markErr), slog.String("event_id", ev.ID.String()))
			}
			return &businessError{err: fmt.Errorf("payin: screening blocked: %s", verdict.Reason)}
		}
	}

	cmd := ledgerclient.Command{
		IdempotencyKey:   fmt.Sprintf("payin:%s:%s", ev.Vendor, ev.VendorEventID),
		IdempotencyScope: "payin:" + ev.Vendor,
		Type:             "money_in",
		Amount:           ev.Amount,
		UserID:           ev.UserID,
		Metadata: map[string]any{
			"gateway":      gateway,
			"external_ref": ev.ExternalRef,
		},
	}

	postErr := m.poster.Post(ctx, cmd)
	if postErr == nil {
		// The ledger idempotency key above makes a redelivered request
		// safe regardless of whether this UPDATE succeeds — money moves
		// exactly once either way, so a failure here is logged, not
		// escalated into a vendor-facing error.
		if markErr := m.repo.MarkPosted(ctx, ev.ID); markErr != nil {
			m.logger.Error("payin: mark posted failed (money already moved, safe to ignore)",
				slog.Any("error", markErr), slog.String("event_id", ev.ID.String()))
		}
		// Best-effort: settle the topup intent this event's reference
		// points at, if any (docs/plan/25 Task T3 step 4). A conditional
		// UPDATE, safe no-op if ExternalRef isn't a topup reference at
		// all, or the intent is already settled — called from both
		// HandleWebhook (fresh delivery) and ReplayEvent (admin retry)
		// via this single shared path, so redelivery/replay always heals
		// a crash that landed between Post succeeding and this running.
		if ev.ExternalRef != "" {
			if _, settleErr := m.repo.MarkTopupIntentSettled(ctx, ev.ExternalRef, ev.ID); settleErr != nil {
				m.logger.Error("payin: mark topup intent settled failed",
					slog.Any("error", settleErr), slog.String("event_id", ev.ID.String()))
			}
		}
		return nil
	}

	if isBusinessFailure(postErr) {
		if markErr := m.repo.MarkFailed(ctx, ev.ID, postErr.Error()); markErr != nil {
			m.logger.Error("payin: mark failed failed", slog.Any("error", markErr), slog.String("event_id", ev.ID.String()))
		}
		// Business failures won't heal on vendor redelivery — the caller
		// (webhook receiver) still acks with 200 so the vendor stops
		// retrying; resolution is an admin replay after the root cause
		// (e.g. suspended account) is fixed.
		return &businessError{err: postErr}
	}

	// Infra failure — event stays 'received', propagate as-is so the
	// webhook receiver returns 503 and the vendor redelivers.
	return fmt.Errorf("payin: post to ledger: %w", postErr)
}

// businessError marks an error as "won't heal on redelivery" for the HTTP
// layer (docs/plan/22 Task T3) without leaking the underlying
// *ledgererr.LedgerError type — that's payin's own classification, made once,
// not re-derived at the transport layer.
type businessError struct{ err error }

func (e *businessError) Error() string { return e.err.Error() }
func (e *businessError) Unwrap() error { return e.err }

// IsBusinessFailure reports whether err (as returned by HandleWebhook or
// ReplayEvent) is a business failure that won't heal on retry — the HTTP
// layer uses this to decide 200-vs-503 (docs/plan/22 Task T2 step 3.5).
func IsBusinessFailure(err error) bool {
	var be *businessError
	return errors.As(err, &be)
}

// isBusinessFailure classifies a raw ledger.Post error — mirrors
// internal/ledger/service/schedule's own isBusinessFailure (docs/plan/19
// Task T1 pattern): apperror.LedgerError (here, its re-export
// ledgererr.LedgerError) means the transaction committed with status='failed'
// (audit trail exists, won't change on retry); anything else is
// structural/infra and IS worth retrying.
func isBusinessFailure(err error) bool {
	var bizErr *ledgererr.LedgerError
	return errors.As(err, &bizErr)
}

// ReplayEvent re-runs the post step for a received/failed event
// (docs/plan/22 Task T4) — idempotent via the same ledger idempotency key
// HandleWebhook uses, so replaying an already-posted event is rejected
// outright (ErrAlreadyPosted) rather than relying on that idempotency as
// the only guard.
func (m *Module) ReplayEvent(ctx context.Context, id uuid.UUID) error {
	ev, err := m.repo.Get(ctx, id)
	if err != nil {
		return err
	}
	if ev.Status == "posted" {
		return ErrAlreadyPosted
	}
	mapping, found, mapErr := m.routing.GetVendorGateway(ctx, ev.Vendor)
	if mapErr != nil {
		return mapErr
	}
	if !found {
		return fmt.Errorf("payin: vendor %q has no gateway mapping configured", ev.Vendor)
	}
	return m.postAndFinalize(ctx, ev, mapping.Gateway)
}

// ListEvents returns webhook events, newest first (docs/plan/22 Task T4
// admin read endpoint). vendor/status empty = no filter on that dimension.
func (m *Module) ListEvents(ctx context.Context, vendor, status string, limit, offset int) ([]WebhookEvent, error) {
	return m.repo.List(ctx, vendor, status, limit, offset)
}

// GetEvent returns one webhook event by id.
func (m *Module) GetEvent(ctx context.Context, id uuid.UUID) (WebhookEvent, error) {
	return m.repo.Get(ctx, id)
}
