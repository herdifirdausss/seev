// Package payin is the public facade for the payin module (docs/roadmap/archive/22
// Task T2, decision K-T2) — consumes normalized VendorService callbacks,
// dedups them, and posts them as money_in to the ledger. This is the ONLY package
// other code may import from internal/payin — importing
// internal/payin/repository or internal/payin/model directly from outside
// this module is a boundary violation (docs/roadmap/archive/01-target-architecture.md,
// enforced by boundary_test.go).
package payin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"google.golang.org/grpc"

	payinv1 "github.com/herdifirdausss/seev/gen/payin/v1"
	"github.com/herdifirdausss/seev/internal/payin/grpcserver"
	"github.com/herdifirdausss/seev/internal/payin/model"
	"github.com/herdifirdausss/seev/internal/payin/repository"
	"github.com/herdifirdausss/seev/internal/vendorgw"
	"github.com/herdifirdausss/seev/pkg/cryptox"
	currencyreg "github.com/herdifirdausss/seev/pkg/currency"
	"github.com/herdifirdausss/seev/pkg/database"
	"github.com/herdifirdausss/seev/pkg/fraudcheck"
	"github.com/herdifirdausss/seev/pkg/generalutil"
	"github.com/herdifirdausss/seev/pkg/ledgerclient"
	"github.com/herdifirdausss/seev/pkg/ledgererr"
)

// Re-exported types so callers never need to import internal/payin/model.
type WebhookEvent = model.WebhookEvent

const (
	VendorCallbackFinalized         = "finalized"
	VendorCallbackAlreadyFinalized  = "already_finalized"
	VendorCallbackIgnored           = "ignored_non_terminal"
	VendorCallbackRecordedUnmatched = "recorded_unmatched"
)

// Poster is the subset of ledger.Module's behavior payin needs — a local
// structural interface rather than a dependency on the concrete
// *ledger.Module type, so unit tests can inject a mock without touching
// Postgres, and so a future HTTP-client shim (docs/roadmap/archive/24, extraction)
// satisfies this same interface with zero payin-side code changes.
type Poster interface {
	Post(ctx context.Context, cmd ledgerclient.Command) error
	// GetUserCurrency resolves the currency CreateTopupIntent records on a
	// new payin_topup_intents row (docs/roadmap/archive/25 Task T3).
	GetUserCurrency(ctx context.Context, userID uuid.UUID, pocketCode string) (string, error)
}

// RegisterGRPC exposes the internal payin service contract.
func (m *Module) RegisterGRPC(server *grpc.Server) {
	payinv1.RegisterPayinServiceServer(server, grpcserver.New(m, ErrTopupIntentNotFound, ErrNoRoute, ErrNoVendorAvailable, ErrScreeningDependencyUnavailable, ErrSandboxVendorUnavailable))
}

// Module is the public facade for the payin module.
type Module struct {
	db            database.DatabaseSQL
	repo          repository.Repository
	routing       repository.RoutingRepository
	poster        Poster
	registry      *vendorgw.Registry
	vendorSession interface {
		CreatePayinSession(context.Context, string, string, decimal.Decimal, string, string) error
	}
	logger *slog.Logger
	// topupTTL is how long a topup intent stays 'pending' before being
	// treated as expired (docs/roadmap/archive/25 Task T3). <=0 falls back to 24h.
	topupTTL time.Duration
	// fraudClient screens deposits before posting (docs/roadmap/archive/37 Task T4).
	// nil is a valid, fully-supported configuration — no screening runs.
	fraudClient *fraudcheck.Client
	// breaker tracks per-vendor circuit health (docs/roadmap/archive/40 Task T1) — nil
	// is a valid, fully-supported configuration (byte-identical to before
	// this feature existed: every registered vendor is always "allowed").
	breaker vendorgw.Breaker
}

// SetVendorSession wires the outbound VendorService seam. It is optional for
// in-process tests and migration compatibility; production composition roots
// set it before serving traffic.
func (m *Module) SetVendorSession(creator interface {
	CreatePayinSession(context.Context, string, string, decimal.Decimal, string, string) error
}) {
	m.vendorSession = creator
}

// NewModule wires the payin module. Vendor and gateway selection comes
// from the routing repository; topupTTL <=0 defaults to 24h. fraudClient
// may be nil to disable pre-posting fraud screening entirely. ring is
// REQUIRED — docs/roadmap/archive/51-a8-data-lifecycle-privacy.md "A8 T2.5b"
// (the contract migration) removed payin_webhook_events.raw's plaintext
// fallback, so there is no longer a valid "cryptox unconfigured" mode to
// construct; repository.NewRepository itself panics on a nil ring as the
// last-resort backstop.
func NewModule(db database.DatabaseSQL, poster Poster, registry *vendorgw.Registry, topupTTL time.Duration, logger *slog.Logger, fraudClient *fraudcheck.Client, breaker vendorgw.Breaker, ring *cryptox.Ring) *Module {
	if logger == nil {
		logger = slog.Default()
	}
	return &Module{
		db:          db,
		repo:        repository.NewRepository(db, ring),
		routing:     repository.NewRoutingRepository(db),
		poster:      poster,
		registry:    registry,
		logger:      logger,
		topupTTL:    topupTTL,
		fraudClient: fraudClient,
		breaker:     breaker,
	}
}

// HandleVendorCallback processes the normalized callback delivered by
// VendorService. Correlation is strictly by the Payin-owned external
// reference and expected vendor/amount/currency; the vendor payload's user id
// is intentionally unavailable on this path.
func (m *Module) HandleVendorCallback(ctx context.Context, vendor, vendorEventID, externalReference, amountRaw, currency, status, occurredAt, inboxID, requestID, unknownStatus string) (string, error) {
	if err := currencyreg.ValidateCode(currency); err != nil {
		return "", fmt.Errorf("payin: invalid normalized callback currency: %w", err)
	}
	amount, err := decimal.NewFromString(amountRaw)
	if err != nil || currencyreg.ValidatePositiveMinorAmount(amount) != nil {
		return "", fmt.Errorf("payin: invalid normalized callback amount")
	}
	mapping, found, err := m.routing.GetVendorGateway(ctx, vendor)
	if err != nil {
		return "", err
	}

	event := model.WebhookEvent{
		ID: generalutil.NewV7(), Vendor: vendor, VendorEventID: vendorEventID,
		ExternalRef: externalReference, Amount: amount, Currency: currency,
		Raw: fmt.Appendf(nil, `{"vendor_inbox_id":%q}`, inboxID), RequestID: requestID,
	}
	unmatchedReason := ""
	var intent model.TopupIntent
	if !found {
		unmatchedReason = "no payin vendor gateway mapping"
	} else {
		intent, found, err = m.repo.GetTopupIntentByReference(ctx, externalReference)
		if err != nil {
			return "", fmt.Errorf("payin: lookup normalized callback intent: %w", err)
		}
		if found {
			intent.NormalizeFinancials()
		}
		switch {
		case !found:
			unmatchedReason = "no matching payin intent"
		case intent.Vendor != vendor:
			unmatchedReason = "callback vendor does not match payin intent"
		case intent.Status != model.TopupStatusPending:
			unmatchedReason = fmt.Sprintf("payin intent is not pending: %s", intent.Status)
		case !intent.ExpiresAt.After(time.Now()):
			unmatchedReason = "payin intent expired"
		case !intent.TotalDebit.Equal(amount) || intent.Currency != currency:
			unmatchedReason = "callback amount or currency does not match payin intent"
		default:
			event.UserID = intent.UserID
			event.MerchantTenantID = intent.MerchantTenantID
			// Keep the immutable Ledger quote/settlement snapshot on the
			// Payin webhook evidence row as well as on the intent. The legacy
			// amount column remains the provider-collected total; the split is
			// carried by the additive fee fields.
			event.FeeAmount = intent.FeeAmount
			event.TotalDebit = intent.TotalDebit
			event.FeeGateway = intent.FeeGateway
			event.FeeQuoteID = intent.FeeQuoteID
			event.FeeRuleID = intent.FeeRuleID
			event.FeeApplication = intent.FeeApplication
			event.FeeQuoteConsumedAt = intent.FeeQuoteConsumedAt
			event.FeeSnapshotVersion = intent.FeeSnapshotVersion
		}
	}

	stored, err := m.repo.GetOrInsert(ctx, event)
	if err != nil {
		return "", fmt.Errorf("payin: persist normalized callback: %w", err)
	}
	if stored.Status == "posted" {
		return VendorCallbackAlreadyFinalized, nil
	}
	if stored.Status == "failed" || stored.Status == "blocked" {
		return VendorCallbackRecordedUnmatched, nil
	}
	if status != "settled" {
		reason := "non-terminal vendor status"
		if unknownStatus != "" {
			reason = "unknown vendor status: " + unknownStatus
		}
		if err := m.repo.MarkFailed(ctx, stored.ID, reason); err != nil {
			return "", err
		}
		return VendorCallbackIgnored, nil
	}
	if unmatchedReason != "" {
		if err := m.repo.MarkFailed(ctx, stored.ID, unmatchedReason); err != nil {
			return "", err
		}
		return VendorCallbackRecordedUnmatched, nil
	}
	if !found {
		return "", fmt.Errorf("payin: normalized callback intent unexpectedly absent")
	}
	if err := m.postAndFinalize(ctx, stored, mapping.Gateway); err != nil {
		if IsBusinessFailure(err) {
			return VendorCallbackRecordedUnmatched, nil
		}
		return "", err
	}
	return VendorCallbackFinalized, nil
}

// postAndFinalize posts stored to the ledger and updates its status
// accordingly. Called both from the normalized callback path and ReplayEvent
// (admin-triggered retry) — identical logic either way.
func (m *Module) postAndFinalize(ctx context.Context, ev model.WebhookEvent, gateway string) error {
	isMerchant := ev.MerchantTenantID != uuid.Nil
	principalAmount := ev.Amount
	feeAmount := decimal.Zero
	totalDebit := ev.Amount
	feeGateway := ""
	feeApplication := model.TopupFeeApplicationAddedOnTop
	var payinID uuid.UUID
	var feeQuoteID *uuid.UUID
	// A retained webhook row is self-describing even when the original intent
	// is no longer readable. Its legacy Amount column is the provider total;
	// recover the principal from the additive C5 snapshot before attempting a
	// replay. Only legacy rows without that snapshot need an intent lookup.
	if !ev.TotalDebit.IsZero() {
		if ev.TotalDebit.IsNegative() || ev.FeeAmount.IsNegative() || ev.FeeAmount.GreaterThanOrEqual(ev.TotalDebit) {
			return &businessError{err: fmt.Errorf("payin: invalid topup financial snapshot")}
		}
		totalDebit = ev.TotalDebit
		feeAmount = ev.FeeAmount
		principalAmount = totalDebit.Sub(feeAmount)
		feeGateway = ev.FeeGateway
		feeQuoteID = ev.FeeQuoteID
		if ev.FeeApplication != "" {
			feeApplication = ev.FeeApplication
		}
	}
	if ev.ExternalRef != "" && ev.TotalDebit.IsZero() {
		if intent, found, lookupErr := m.repo.GetTopupIntentByReference(ctx, ev.ExternalRef); lookupErr != nil {
			return fmt.Errorf("payin: load topup financial snapshot: %w", lookupErr)
		} else if found {
			intent.NormalizeFinancials()
			if !intent.TotalDebit.Equal(ev.Amount) || intent.Currency != ev.Currency {
				return &businessError{err: fmt.Errorf("payin: settled amount no longer matches topup financial snapshot")}
			}
			principalAmount = intent.Amount
			payinID = intent.ID
			feeQuoteID = intent.FeeQuoteID
			feeAmount = intent.FeeAmount
			totalDebit = intent.TotalDebit
			feeGateway = intent.FeeGateway
			if intent.Gateway != "" {
				gateway = intent.Gateway
			}
			if intent.FeeApplication != "" {
				feeApplication = intent.FeeApplication
			}
		}
	}
	// Fraud screening is deliberately SKIPPED for merchant-owned events
	// (Plan 57 T6 scope decision): fraudClient.Check is keyed on a single
	// userID, and every merchant event's UserID is the zero sentinel — running
	// it unmodified would silently pool every merchant tenant into one
	// shared "zero user" velocity bucket, a real correctness bug, not a
	// missing nice-to-have. Merchant-specific fraud/velocity screening is
	// out of scope for T6.
	if m.fraudClient != nil && !isMerchant {
		// C5's velocity policy measures the provider debit, not just the wallet
		// principal. For legacy events these values are identical.
		verdict, ferr := m.fraudClient.Check(ctx, "topup", "money_in", ev.UserID, totalDebit, ev.Currency)
		if ferr != nil {
			if errors.Is(ferr, fraudcheck.ErrDependencyUnavailable) {
				// docs/roadmap/archive/45 Task T3/K4: fraud-service is reachable but
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

	idempotencyKey := fmt.Sprintf("payin:%s:%s", ev.Vendor, ev.VendorEventID)
	idempotencyScope := "payin:" + ev.Vendor
	if payinID != uuid.Nil {
		idempotencyKey = "payin:" + payinID.String()
		idempotencyScope = "payin:" + payinID.String()
	}
	cmd := ledgerclient.Command{
		IdempotencyKey:   idempotencyKey,
		IdempotencyScope: idempotencyScope,
		Type:             "money_in",
		Amount:           principalAmount,
		UserID:           ev.UserID,
		Currency:         ev.Currency,
		Metadata: map[string]any{
			"gateway":            gateway,
			"external_ref":       ev.ExternalRef,
			"currency_inflight":  true,
			"provider_reference": ev.ExternalRef,
			"currency":           ev.Currency,
			"total_debit":        totalDebit.String(),
			"fee_amount":         feeAmount.String(),
			"fee_application":    feeApplication,
		},
	}
	if payinID != uuid.Nil {
		cmd.Metadata["payin_id"] = payinID.String()
	}
	if feeQuoteID != nil {
		cmd.Metadata["fee_quote_id"] = feeQuoteID.String()
	}
	if feeAmount.IsPositive() {
		if feeGateway == "" {
			return &businessError{err: fmt.Errorf("payin: topup fee snapshot has no fee gateway")}
		}
		cmd.Metadata["fee_gateway"] = feeGateway
	}
	if isMerchant {
		// Plan 57 T6: the merchant-owned counterpart of money_in — same
		// idempotency key shape (still scoped by vendor+vendor_event_id,
		// owner-neutral already), different ledger processor type and a
		// MerchantTenantID instead of UserID so the destination resolves
		// to the tenant's own cash account, never a caller-supplied one.
		cmd.Type = "merchant_payin_credit"
		cmd.Amount = totalDebit
		cmd.UserID = uuid.Nil
		cmd.MerchantTenantID = ev.MerchantTenantID
	}

	postErr := m.poster.Post(ctx, cmd)
	if postErr == nil || isLedgerAlreadyPosted(postErr) {
		// The ledger idempotency key above makes a redelivered request
		// safe regardless of whether this UPDATE succeeds — money moves
		// exactly once either way, so a failure here is logged, not
		// escalated into a vendor-facing error.
		if markErr := m.repo.MarkPosted(ctx, ev.ID); markErr != nil {
			m.logger.Error("payin: mark posted failed (money already moved, safe to ignore)",
				slog.Any("error", markErr), slog.String("event_id", ev.ID.String()))
		}
		// Best-effort: settle the topup intent this event's reference
		// points at, if any (docs/roadmap/archive/25 Task T3 step 4). A conditional
		// UPDATE, safe no-op if ExternalRef isn't a topup reference at
		// all, or the intent is already settled — called from both
		// normalized callback delivery and ReplayEvent (admin retry)
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
// layer (docs/roadmap/archive/22 Task T3) without leaking the underlying
// *ledgererr.LedgerError type — that's payin's own classification, made once,
// not re-derived at the transport layer.
type businessError struct{ err error }

func (e *businessError) Error() string { return e.err.Error() }
func (e *businessError) Unwrap() error { return e.err }

// IsBusinessFailure reports whether err (as returned by the normalized callback
// path or ReplayEvent) is a business failure that won't heal on retry — the HTTP
// layer uses this to decide 200-vs-503 (docs/roadmap/archive/22 Task T2 step 3.5).
func IsBusinessFailure(err error) bool {
	var be *businessError
	return errors.As(err, &be)
}

// isBusinessFailure classifies a raw ledger.Post error — mirrors
// internal/ledger/service/schedule's own isBusinessFailure (docs/roadmap/archive/19
// Task T1 pattern): apperror.LedgerError (here, its re-export
// ledgererr.LedgerError) means the transaction committed with status='failed'
// (audit trail exists, won't change on retry); anything else is
// structural/infra and IS worth retrying.
func isBusinessFailure(err error) bool {
	var bizErr *ledgererr.LedgerError
	return errors.As(err, &bizErr)
}

func isLedgerAlreadyPosted(err error) bool {
	var ledgerError *ledgererr.LedgerError
	return errors.As(err, &ledgerError) && ledgerError.Code == "ALREADY_POSTED"
}

// ReplayEvent re-runs the post step for a received/failed event
// (docs/roadmap/archive/22 Task T4) — idempotent via the same ledger idempotency key
// The same idempotency key is used by callback processing, so replaying an already-posted event is rejected
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

// ListEvents returns webhook events, newest first (docs/roadmap/archive/22 Task T4
// admin read endpoint). vendor/status empty = no filter on that dimension.
func (m *Module) ListEvents(ctx context.Context, vendor, status string, limit, offset int) ([]WebhookEvent, error) {
	return m.repo.List(ctx, vendor, status, limit, offset)
}

// GetEvent returns one webhook event by id.
func (m *Module) GetEvent(ctx context.Context, id uuid.UUID) (WebhookEvent, error) {
	return m.repo.Get(ctx, id)
}
