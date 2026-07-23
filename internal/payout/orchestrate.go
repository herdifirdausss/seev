package payout

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/herdifirdausss/seev/internal/payout/model"
	"github.com/herdifirdausss/seev/internal/vendorgw"
	"github.com/herdifirdausss/seev/pkg/fraudcheck"
	"github.com/herdifirdausss/seev/pkg/generalutil"
	"github.com/herdifirdausss/seev/pkg/ledgerclient"
	"github.com/herdifirdausss/seev/pkg/ledgererr"
	"github.com/herdifirdausss/seev/pkg/middleware"
)

// payoutScope is the fixed idempotency scope for every ledger command this
// module posts (docs/roadmap/archive/23 Task T3 step 1) — not per-vendor, unlike
// payin's "payin:<vendor>" (docs/roadmap/archive/22 Task T2): a payout request's ID is
// already globally unique and embedded in every key below, so a shared
// scope adds no collision risk and keeps the three keys for one request
// trivially greppable together.
const payoutScope = "payout"

func holdIdempotencyKey(id uuid.UUID) string   { return "payout:" + id.String() + ":hold" }
func settleIdempotencyKey(id uuid.UUID) string { return "payout:" + id.String() + ":settle" }
func cancelIdempotencyKey(id uuid.UUID) string { return "payout:" + id.String() + ":cancel" }

// Create starts a new payout request (docs/roadmap/archive/23 Task T3 step 1):
// created -> held (blocking withdraw_initiate) -> submitted -> vendor
// Submit. The request ID is returned even if hold or the initial submit
// attempt fails partway — HandleWebhook/the resume job (Task T3 step 3)
// continues the SAME request from wherever it stopped, using the request
// ID as every subsequent ledger idempotency key; nothing here is a "start
// over from scratch" operation.
func (m *Module) Create(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, destination []byte, createdBy, quoteID string) (uuid.UUID, error) {
	if err := m.ensureIntakeOpen(ctx); err != nil {
		return uuid.Nil, err
	}
	currency, err := m.poster.GetUserCurrency(ctx, userID, "")
	if err != nil {
		return uuid.Nil, fmt.Errorf("payout: resolve user currency: %w", err)
	}

	if m.fraudClient != nil {
		verdict, ferr := m.fraudClient.Check(ctx, "payout", "withdraw_initiate", userID, amount, currency)
		if ferr != nil {
			if errors.Is(ferr, fraudcheck.ErrDependencyUnavailable) {
				// docs/roadmap/archive/45 Task T3/K4: fraud-service is reachable but its
				// velocity dependency is down — fail CLOSED, unlike a
				// generic infra error below (fail open). No row inserted,
				// no hold posted.
				m.logger.Warn("payout: screening dependency unavailable, failing closed", slog.String("user_id", userID.String()))
				return uuid.Nil, ErrScreeningDependencyUnavailable
			}
			// Infra failure — fail open: proceed as if unscreened rather
			// than block a legitimate withdrawal over a screening outage.
			m.logger.Error("payout: screening check error, failing open", slog.Any("error", ferr), slog.String("user_id", userID.String()))
		} else if verdict.Block {
			// Fail-closed: NO payout_requests row is ever inserted for a
			// blocked attempt — the audit trail lives only in
			// fraud-service's own screening_events. Money was never held.
			return uuid.Nil, fmt.Errorf("%w: %s", ErrScreeningBlocked, verdict.Reason)
		}
	}

	vendor, _, err := m.ResolvePayoutRoute(ctx, userID, currency, amount, nil)
	if err != nil {
		return uuid.Nil, err
	}

	id := generalutil.NewV7()
	req := model.PayoutRequest{
		ID: id, UserID: userID, Amount: amount, Currency: currency,
		Vendor: vendor, Destination: destination, CreatedBy: createdBy,
		RequestID: middleware.RequestIDFromCtx(ctx),
	}
	if err := m.repo.Insert(ctx, req); err != nil {
		return uuid.Nil, fmt.Errorf("payout: insert request: %w", err)
	}

	// Fee quote consumption (docs/roadmap/archive/38 Task T5) — ANTI-BURN ordering:
	// AFTER the row exists but BEFORE any hold is posted, so a rejected
	// quote (expired/mismatch) never touches money — worst case cost is
	// one re-quote, never a stranded hold. txType "withdraw_settle"/gateway
	// "" matches settle()'s own ResolveFee call below exactly (the platform
	// fee doesn't vary by bank rail, docs/roadmap/archive/25 Task T2).
	if quoteID != "" {
		parsedQuoteID, perr := uuid.Parse(quoteID)
		if perr != nil {
			if _, tErr := m.repo.TransitionToRejected(ctx, id, "quote_id is not a valid UUID"); tErr != nil {
				m.logger.Error("payout: mark rejected failed", slog.Any("error", tErr), slog.String("request_id", id.String()))
			}
			return id, fmt.Errorf("payout: quote_id is not a valid UUID")
		}
		fee, feeGateway, qerr := m.poster.ConsumeFeeQuote(ctx, parsedQuoteID, userID, "withdraw_settle", currency, amount, "payout:"+id.String())
		if qerr != nil {
			if _, tErr := m.repo.TransitionToRejected(ctx, id, qerr.Error()); tErr != nil {
				m.logger.Error("payout: mark rejected failed", slog.Any("error", tErr), slog.String("request_id", id.String()))
			}
			return id, qerr
		}
		if sErr := m.repo.SetQuotedFee(ctx, id, parsedQuoteID, fee, feeGateway); sErr != nil {
			// The quote is already consumed (single-use) at this point — we
			// cannot safely retry consumption. Reject rather than silently
			// fall back to ResolveFee at settle time, which would break the
			// quote's entire promise (an unresolvable inconsistency: quote
			// spent, but its fee never recorded on this row).
			if _, tErr := m.repo.TransitionToRejected(ctx, id, "failed to persist quoted fee"); tErr != nil {
				m.logger.Error("payout: mark rejected failed", slog.Any("error", tErr), slog.String("request_id", id.String()))
			}
			return id, fmt.Errorf("payout: persist quoted fee: %w", sErr)
		}
	}

	if err := m.hold(ctx, req); err != nil {
		return id, fmt.Errorf("payout: hold: %w", err)
	}

	if err := m.enqueueSubmit(ctx, id, vendor); err != nil {
		// Hold already succeeded — money is safely parked in the user's
		// hold account. An enqueue failure here is not a Create-level
		// error; the resume job (step 3) retries the enqueue, which is
		// itself idempotent (EnqueueInitialSubmit's transition guard).
		m.logger.Error("payout: initial enqueue submit failed, resume job will retry",
			slog.Any("error", err), slog.String("request_id", id.String()))
	}

	return id, nil
}

// hold posts withdraw_initiate and records hold_tx_id + created->held.
// Safe to call again for the same request (crash-mid-flight resume,
// docs/roadmap/archive/23 Task T6) — ledger.Post's own idempotency key is the money-
// safety guarantee; TransitionToHeld's conditional UPDATE just means a
// second call here is a harmless no-op once the first has landed.
func (m *Module) hold(ctx context.Context, req model.PayoutRequest) error {
	key := holdIdempotencyKey(req.ID)
	cmd := ledgerclient.Command{
		IdempotencyKey: key, IdempotencyScope: payoutScope,
		Type: "withdraw_initiate", Amount: req.Amount, UserID: req.UserID,
	}
	if err := m.poster.Post(ctx, cmd); err != nil {
		return err
	}
	tx, err := m.poster.GetTransactionByIdempotencyKey(ctx, key, payoutScope)
	if err != nil {
		return fmt.Errorf("resolve hold tx id: %w", err)
	}
	if _, err := m.repo.TransitionToHeld(ctx, req.ID, tx.ID); err != nil {
		return fmt.Errorf("transition to held: %w", err)
	}
	return nil
}

// classifySubmitOutcome maps one Submit round-trip to the outcome
// classification the anti-double-payout failover rule (mayFailover) reads
// from payout_vendor_calls (docs/roadmap/archive/40 Task T3). callErr non-nil is a
// transport/infra failure (timeout, 5xx, unknown) — the vendor may or may
// not have received the request, so it's "uncertain", never "rejected"
// (gotcha #13 master: only a definitive SYNCHRONOUS business rejection,
// vendorgw.PayoutFailed with NO error, counts as "rejected" and is safe to
// fail over from).
func classifySubmitOutcome(result vendorgw.PayoutResult, callErr error) string {
	if callErr != nil {
		return model.VendorCallUncertain
	}
	if result.Status == vendorgw.PayoutFailed {
		return model.VendorCallRejected
	}
	return model.VendorCallAccepted
}

// classifyQueryOutcome mirrors classifySubmitOutcome for the resume job's
// polling calls (pollVendorPending) — a request only ever reaches Query
// after an earlier Submit already landed "accepted", so a successful Query
// (any status) can only ever confirm "accepted", never newly "rejected".
func classifyQueryOutcome(callErr error) string {
	if callErr != nil {
		return model.VendorCallUncertain
	}
	return model.VendorCallAccepted
}

// mayFailover implements docs/roadmap/archive/40's locked anti-double-payout rule
// EXACTLY: switching this request to a different vendor is safe ONLY while
// none of its vendor calls has EVER landed accepted or uncertain — once
// one has, the request is PINNED to that vendor forever (recovery =
// Query/retry the SAME vendor via the resume job, never a new one). A
// synchronous business rejection ("rejected") never blocks failover.
func mayFailover(calls []model.PayoutVendorCall) bool {
	for _, c := range calls {
		if c.Outcome == model.VendorCallAccepted || c.Outcome == model.VendorCallUncertain {
			return false
		}
	}
	return true
}

// enqueueSubmit moves held/vendor_pending -> submitted and durably enqueues
// the first vendor dispatch command, atomically (docs/roadmap/archive/45 Task T1/K1)
// — relay.go's dispatchOne is the ONLY place that ever calls
// provider.Submit from here on; this call just makes the work durable and
// returns immediately. won=false (the transition's own guard didn't match)
// is surfaced as ErrInvalidTransition rather than silently ignored, same
// spirit as every other Transition* caller in this file — callers that
// consider a lost race benign (the resume job) already treat it that way.
func (m *Module) enqueueSubmit(ctx context.Context, id uuid.UUID, vendor string) error {
	won, err := m.commandRepo.EnqueueInitialSubmit(ctx, id, vendor)
	if err != nil {
		return fmt.Errorf("payout: enqueue initial submit: %w", err)
	}
	if !won {
		return fmt.Errorf("%w: cannot submit request in current status", ErrInvalidTransition)
	}
	return nil
}

// ensureSubmitCommand is the resume job's idempotent recovery for a
// 'held'/'submitted' request that currently has no live command
// (docs/roadmap/archive/45 Task T1/K1) — safe to call speculatively; EnsureSubmitCommand
// itself no-ops under multi-replica races via a conflicting insert.
func (m *Module) ensureSubmitCommand(ctx context.Context, id uuid.UUID, vendor string) error {
	if _, err := m.commandRepo.EnsureSubmitCommand(ctx, id, vendor); err != nil {
		return fmt.Errorf("payout: ensure submit command: %w", err)
	}
	return nil
}

// settle posts withdraw_settle (docs/roadmap/archive/23 Task T4) and moves the
// request to 'settled'. NEVER builds its own double-settle protection —
// the ledger's closed_by_tx_id guard (K3) is the sole source of truth;
// losing that race (ledgererr.ErrAlreadyClosed) is reconciled, not treated as
// this call's own failure.
func (m *Module) settle(ctx context.Context, id uuid.UUID, gateway string) error {
	req, err := m.repo.Get(ctx, id)
	if err != nil {
		return err
	}
	if req.HoldTxID == nil {
		return fmt.Errorf("payout: settle: request %s has no hold_tx_id", id)
	}

	key := settleIdempotencyKey(id)
	metadata := map[string]any{"gateway": gateway}
	// Withdraw fee is priced and charged HERE, on settle, never on hold —
	// see Poster.ResolveFee's own doc comment for why. Gateway key "" for
	// the fee lookup: the platform fee doesn't vary by bank rail, only by
	// transaction type/currency (docs/roadmap/archive/25 Task T2).
	//
	// docs/roadmap/archive/38 Task T5: a request created WITH a fee quote already has
	// its fee LOCKED IN at Create time (req.FeeQuoteID != nil) — settle
	// uses that stored value verbatim and skips ResolveFee entirely, no
	// matter how much later settle runs (the resume job may fire hours
	// after Create) or what fee_rules looks like by then. A request
	// created WITHOUT a quote falls back to ResolveFee exactly as before
	// this feature existed — the TTL on a quote only ever gates Create,
	// never settle.
	var fee decimal.Decimal
	var feeGateway string
	var feeOK bool
	if req.FeeQuoteID != nil {
		fee, feeGateway, feeOK = *req.FeeAmount, req.FeeGateway, req.FeeAmount.IsPositive()
	} else {
		var feeErr error
		fee, feeGateway, feeOK, feeErr = m.poster.ResolveFee(ctx, req.UserID, "withdraw_settle", "", req.Currency, req.Amount)
		if feeErr != nil {
			return fmt.Errorf("payout: resolve fee: %w", feeErr)
		}
	}
	if feeOK {
		metadata["fee_amount"] = fee.String()
		metadata["fee_gateway"] = feeGateway
	}
	cmd := ledgerclient.Command{
		IdempotencyKey: key, IdempotencyScope: payoutScope,
		Type: "withdraw_settle", Amount: req.Amount, UserID: req.UserID,
		ReferenceID: *req.HoldTxID, Metadata: metadata,
	}
	if postErr := m.poster.Post(ctx, cmd); postErr != nil {
		if errors.Is(postErr, ledgererr.ErrAlreadyClosed) {
			return m.reconcileAfterLostRace(ctx, id, postErr)
		}
		if setErr := m.repo.SetError(ctx, id, postErr.Error()); setErr != nil {
			m.logger.Error("payout: set error failed", slog.Any("error", setErr), slog.String("request_id", id.String()))
		}
		return postErr
	}

	tx, err := m.poster.GetTransactionByIdempotencyKey(ctx, key, payoutScope)
	if err != nil {
		return fmt.Errorf("resolve settle tx id: %w", err)
	}
	if _, err := m.repo.TransitionToSettled(ctx, id, tx.ID); err != nil {
		return fmt.Errorf("transition to settled: %w", err)
	}
	return nil
}

// cancel posts withdraw_cancel (docs/roadmap/archive/23 Task T4) and moves the
// request to 'cancelled'. Same K3-guard-only philosophy as settle.
func (m *Module) cancel(ctx context.Context, id uuid.UUID, gateway, reason string) error {
	req, err := m.repo.Get(ctx, id)
	if err != nil {
		return err
	}
	if req.HoldTxID == nil {
		return fmt.Errorf("payout: cancel: request %s has no hold_tx_id", id)
	}

	key := cancelIdempotencyKey(id)
	cmd := ledgerclient.Command{
		IdempotencyKey: key, IdempotencyScope: payoutScope,
		Type: "withdraw_cancel", Amount: req.Amount, UserID: req.UserID,
		ReferenceID: *req.HoldTxID, Metadata: map[string]any{"gateway": gateway},
	}
	if postErr := m.poster.Post(ctx, cmd); postErr != nil {
		if errors.Is(postErr, ledgererr.ErrAlreadyClosed) {
			return m.reconcileAfterLostRace(ctx, id, postErr)
		}
		if setErr := m.repo.SetError(ctx, id, postErr.Error()); setErr != nil {
			m.logger.Error("payout: set error failed", slog.Any("error", setErr), slog.String("request_id", id.String()))
		}
		return postErr
	}

	tx, err := m.poster.GetTransactionByIdempotencyKey(ctx, key, payoutScope)
	if err != nil {
		return fmt.Errorf("resolve cancel tx id: %w", err)
	}
	if _, err := m.repo.TransitionToCancelled(ctx, id, tx.ID); err != nil {
		return fmt.Errorf("transition to cancelled: %w", err)
	}
	if reason != "" {
		if setErr := m.repo.SetError(ctx, id, reason); setErr != nil {
			m.logger.Error("payout: set error failed", slog.Any("error", setErr), slog.String("request_id", id.String()))
		}
	}
	return nil
}

// reconcileAfterLostRace handles ledgererr.ErrAlreadyClosed (docs/roadmap/archive/23
// Task T4): this call's own settle/cancel attempt lost the ledger's K3
// race to a DIFFERENT lifecycle-closing command (a concurrent settle vs.
// cancel, or a redelivered callback arriving after an admin cancel already
// closed hold_tx_id). It is NOT an error from the caller's point of view —
// the request reached a terminal state via the other path; this just logs
// the conflict and lets the request's current (already-correct) status
// stand, matching the loser's local read model to reality rather than
// overwriting it.
func (m *Module) reconcileAfterLostRace(ctx context.Context, id uuid.UUID, cause error) error {
	req, err := m.repo.Get(ctx, id)
	if err != nil {
		return fmt.Errorf("reconcile after lost race: %w", err)
	}
	m.logger.Warn("payout: lost ledger close race, reconciling to existing status",
		slog.String("request_id", id.String()), slog.String("status", req.Status), slog.Any("cause", cause))
	if setErr := m.repo.SetError(ctx, id, "lost race to close hold: "+cause.Error()); setErr != nil {
		m.logger.Error("payout: set error failed during reconcile", slog.Any("error", setErr), slog.String("request_id", id.String()))
	}
	return nil
}

func (m *Module) recordVendorCall(ctx context.Context, requestID uuid.UUID, summary string, result vendorgw.PayoutResult, callErr error, outcome string) error {
	call := model.PayoutVendorCall{
		ID: generalutil.NewV7(), PayoutRequestID: requestID, Attempt: 1, ReqSummary: summary, Outcome: outcome,
	}
	if callErr != nil {
		call.Error = callErr.Error()
	} else {
		call.RespStatus = string(result.Status)
	}
	if err := m.repo.InsertVendorCall(ctx, call); err != nil {
		return fmt.Errorf("payout: persist vendor call outcome: %w", err)
	}
	return nil
}

// ResumeStuck re-drives requests that crashed or stalled mid-flight
// (docs/roadmap/archive/23 Task T3 step 3 — "jawaban crash-mid-flight: state machine +
// job resume, bukan saga framework"), covering EVERY non-terminal status a
// crash can leave a request in, not just 'submitted'/'vendor_pending' — a
// crash right after 'created' (before hold() ever ran) or right after
// 'held' (money already parked in the hold account, but before
// TransitionToSubmitted) would otherwise leave a request permanently
// orphaned with no resume path (docs/roadmap/archive/23 Task T6's own chaos scenario
// is what surfaced this: those are two of the four required kill points):
//
//   - 'created': hold() retried from scratch (idempotent — deterministic
//     ledger idempotency key), then falls through into submit() so a
//     single resume pass can carry a request all the way to a terminal
//     state without waiting for a second pass.
//   - 'held'/'submitted': submit() retried — it accepts either as a valid
//     starting status (TransitionToSubmitted's own predecessor set is
//     ('held','vendor_pending'), and Submit is idempotent by request ID
//     regardless), whether the crash landed right after the hold or right
//     after transitioning to submitted but before the vendor call's
//     outcome was recorded.
//   - 'vendor_pending': Query'd and routed to a terminal state exactly
//     like submit() routes a fresh Submit result.
//
// Called by the resume job (internal/payout/worker) on a tight interval,
// and directly by chaos tests (Task T6) after a simulated crash.
func (m *Module) ResumeStuck(ctx context.Context, olderThan time.Duration) (resumed, failed int, err error) {
	cutoff := time.Now().Add(-olderThan)

	createdStuck, err := m.repo.ListStuck(ctx, model.StatusCreated, cutoff, 100)
	if err != nil {
		return 0, 0, fmt.Errorf("payout: resume: list stuck created: %w", err)
	}
	for _, req := range createdStuck {
		if err := m.hold(ctx, req); err != nil {
			m.logger.Error("payout-resume: retry hold failed",
				slog.Any("error", err), slog.String("request_id", req.ID.String()))
			failed++
			continue
		}
		if err := m.enqueueSubmit(ctx, req.ID, req.Vendor); err != nil {
			m.logger.Error("payout-resume: retry enqueue submit (from created) failed",
				slog.Any("error", err), slog.String("request_id", req.ID.String()))
			failed++
			continue
		}
		resumed++
	}

	heldStuck, err := m.repo.ListStuck(ctx, model.StatusHeld, cutoff, 100)
	if err != nil {
		return resumed, failed, fmt.Errorf("payout: resume: list stuck held: %w", err)
	}
	for _, req := range heldStuck {
		// A crash between hold() succeeding and EnqueueInitialSubmit's own
		// transaction is the exact gap this recovers — the money is
		// already parked, the request just never made it to 'submitted'
		// with a first command.
		if err := m.enqueueSubmit(ctx, req.ID, req.Vendor); err != nil {
			m.logger.Error("payout-resume: retry enqueue submit (from held) failed",
				slog.Any("error", err), slog.String("request_id", req.ID.String()))
			failed++
			continue
		}
		resumed++
	}

	submittedStuck, err := m.repo.ListStuck(ctx, model.StatusSubmitted, cutoff, 100)
	if err != nil {
		return resumed, failed, fmt.Errorf("payout: resume: list stuck submitted: %w", err)
	}
	for _, req := range submittedStuck {
		// docs/roadmap/archive/45 Task T1/K2: resume's job here is only to ensure a
		// command exists at all, live OR dead-and-operator-visible — a live
		// command is already being retried on its own backoff by the
		// relay, and a dead one is a deliberate stop for a human, neither
		// should be silently re-enqueued by this automatic pass. Every
		// other case (no command ever recorded, or the most recent one
		// simply completed toward some other status) is a genuine gap
		// worth a fresh command.
		if _, live, liveErr := m.commandRepo.GetLiveCommand(ctx, req.ID); liveErr != nil {
			m.logger.Error("payout-resume: get live command failed", slog.Any("error", liveErr), slog.String("request_id", req.ID.String()))
			failed++
			continue
		} else if live {
			resumed++
			continue
		}
		dead, err := m.commandRepo.HasDeadCommand(ctx, req.ID)
		if err != nil {
			m.logger.Error("payout-resume: has dead command failed", slog.Any("error", err), slog.String("request_id", req.ID.String()))
			failed++
			continue
		}
		if dead {
			// The most recent command dead-lettered — visible to the
			// operator, left alone (AdminRetry is the deliberate human
			// action to revive it).
			resumed++
			continue
		}
		if err := m.ensureSubmitCommand(ctx, req.ID, req.Vendor); err != nil {
			m.logger.Error("payout-resume: ensure submit command (from submitted) failed",
				slog.Any("error", err), slog.String("request_id", req.ID.String()))
			failed++
			continue
		}
		resumed++
	}

	pendingStuck, err := m.repo.ListStuck(ctx, model.StatusVendorPending, cutoff, 100)
	if err != nil {
		return resumed, failed, fmt.Errorf("payout: resume: list stuck vendor_pending: %w", err)
	}
	for _, req := range pendingStuck {
		if err := m.pollVendorPending(ctx, req); err != nil {
			m.logger.Error("payout-resume: poll vendor_pending failed",
				slog.Any("error", err), slog.String("request_id", req.ID.String()))
			failed++
			continue
		}
		resumed++
	}

	return resumed, failed, nil
}

// stuckStatuses is every status the resume job re-drives (docs/roadmap/archive/23
// Task T3) — the exhaustive set CountStuck reports on for the
// payout_stuck_requests gauge (docs/roadmap/archive/43 K5).
var stuckStatuses = []string{model.StatusCreated, model.StatusHeld, model.StatusSubmitted, model.StatusVendorPending}

// CountStuck reports the FULL backlog per status (no 100-row cap, unlike
// ResumeStuck's per-pass ListStuck calls) — feeds the payout_stuck_requests
// gauge (docs/roadmap/archive/43 K5). Every status in stuckStatuses is present in the
// result, 0 when the repository reported none, so the gauge always reflects
// the true state instead of silently going stale for an empty status.
func (m *Module) CountStuck(ctx context.Context, olderThan time.Duration) (map[string]int, error) {
	cutoff := time.Now().Add(-olderThan)
	counts, err := m.repo.CountStuck(ctx, stuckStatuses, cutoff)
	if err != nil {
		return nil, fmt.Errorf("payout: count stuck: %w", err)
	}
	out := make(map[string]int, len(stuckStatuses))
	for _, status := range stuckStatuses {
		out[status] = counts[status]
	}
	return out, nil
}

// pollVendorPending queries the vendor for a 'vendor_pending' request's
// current status and routes the result to a terminal state — the polling
// counterpart to submit()'s inline routing of a fresh Submit result.
// PayoutPending is not an error here: the vendor genuinely hasn't resolved
// the payout yet, so the request simply stays vendor_pending until the next
// resume pass.
func (m *Module) pollVendorPending(ctx context.Context, req model.PayoutRequest) error {
	provider, ok := m.registry.Payout(req.Vendor)
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownVendor, req.Vendor)
	}

	result, queryErr := provider.Query(ctx, req.ID.String())
	if err := m.recordVendorCall(ctx, req.ID, fmt.Sprintf("query vendor=%s vendor_ref=%s", req.Vendor, req.VendorRef), result, queryErr, classifyQueryOutcome(queryErr)); err != nil {
		return err
	}
	if queryErr != nil {
		return queryErr
	}

	gateway, err := m.gatewayForVendor(ctx, req.Vendor)
	if err != nil {
		return err
	}
	switch result.Status {
	case vendorgw.PayoutSettled:
		return m.settle(ctx, req.ID, gateway)
	case vendorgw.PayoutFailed:
		return m.cancel(ctx, req.ID, gateway, result.Reason)
	case vendorgw.PayoutPending:
		return nil
	default:
		return fmt.Errorf("payout: unknown vendor result status %q", result.Status)
	}
}

// AdminCancel cancels a stuck request via an admin action (docs/roadmap/archive/23
// Task T5) — returns the held amount to the user's cash account through
// the same withdraw_cancel path submit() itself uses when a vendor
// synchronously rejects a payout. Only requests already in a
// vendor-contacted state (submitted/vendor_pending) are cancellable — the
// same predecessor set TransitionToCancelled enforces at the DB level —
// checked here first so the caller gets ErrInvalidTransition instead of a
// confusing ledger-level rejection.
func (m *Module) AdminCancel(ctx context.Context, id uuid.UUID, reason string) error {
	req, err := m.repo.Get(ctx, id)
	if err != nil {
		return err
	}
	if req.Status != model.StatusSubmitted && req.Status != model.StatusVendorPending {
		return fmt.Errorf("%w: cannot cancel request in status %q", ErrInvalidTransition, req.Status)
	}
	gateway, err := m.gatewayForVendor(ctx, req.Vendor)
	if err != nil {
		return err
	}
	return m.cancel(ctx, id, gateway, reason)
}

// AdminRetry re-submits a request stuck in 'submitted' (docs/roadmap/archive/23 Task
// T5), exposed as a manual action for an operator who doesn't want to
// wait. Unlike the automatic resume job (which deliberately leaves a
// dead-lettered command alone for the operator to see, docs/roadmap/archive/45 Task
// T1/K2), THIS is the operator's own decision to act: if no live command
// exists — whether because none was ever created or because the previous
// one dead-lettered after exhausting its retry budget — a fresh command
// (a new attempt, a clean retry budget) is enqueued. A live command already
// in flight or backing off is left alone; there is nothing to force.
func (m *Module) AdminRetry(ctx context.Context, id uuid.UUID) error {
	req, err := m.repo.Get(ctx, id)
	if err != nil {
		return err
	}
	if req.Status != model.StatusSubmitted {
		return fmt.Errorf("%w: cannot retry request in status %q", ErrInvalidTransition, req.Status)
	}
	if _, live, err := m.commandRepo.GetLiveCommand(ctx, id); err != nil {
		return fmt.Errorf("payout: admin retry: get live command: %w", err)
	} else if live {
		return nil
	}
	return m.ensureSubmitCommand(ctx, id, req.Vendor)
}

// Get returns one payout request by id.
func (m *Module) Get(ctx context.Context, id uuid.UUID) (PayoutRequest, error) {
	return m.repo.Get(ctx, id)
}

// List returns payout requests newest first (docs/roadmap/archive/23 Task T5 admin
// endpoint). status/vendor empty = no filter.
func (m *Module) List(ctx context.Context, status, vendor string, limit, offset int) ([]PayoutRequest, error) {
	return m.repo.List(ctx, status, vendor, limit, offset)
}
