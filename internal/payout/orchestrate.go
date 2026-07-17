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
	"github.com/herdifirdausss/seev/pkg/generalutil"
	"github.com/herdifirdausss/seev/pkg/ledgerclient"
	"github.com/herdifirdausss/seev/pkg/ledgererr"
	"github.com/herdifirdausss/seev/pkg/middleware"
)

// payoutScope is the fixed idempotency scope for every ledger command this
// module posts (docs/plan/23 Task T3 step 1) — not per-vendor, unlike
// payin's "payin:<vendor>" (docs/plan/22 Task T2): a payout request's ID is
// already globally unique and embedded in every key below, so a shared
// scope adds no collision risk and keeps the three keys for one request
// trivially greppable together.
const payoutScope = "payout"

func holdIdempotencyKey(id uuid.UUID) string   { return "payout:" + id.String() + ":hold" }
func settleIdempotencyKey(id uuid.UUID) string { return "payout:" + id.String() + ":settle" }
func cancelIdempotencyKey(id uuid.UUID) string { return "payout:" + id.String() + ":cancel" }

// Create starts a new payout request (docs/plan/23 Task T3 step 1):
// created -> held (blocking withdraw_initiate) -> submitted -> vendor
// Submit. The request ID is returned even if hold or the initial submit
// attempt fails partway — HandleWebhook/the resume job (Task T3 step 3)
// continues the SAME request from wherever it stopped, using the request
// ID as every subsequent ledger idempotency key; nothing here is a "start
// over from scratch" operation.
func (m *Module) Create(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, destination []byte, createdBy, quoteID string) (uuid.UUID, error) {
	currency, err := m.poster.GetUserCurrency(ctx, userID, "")
	if err != nil {
		return uuid.Nil, fmt.Errorf("payout: resolve user currency: %w", err)
	}

	if m.fraudClient != nil {
		verdict, ferr := m.fraudClient.Check(ctx, "payout", "withdraw_initiate", userID, amount, currency)
		if ferr != nil {
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

	// Fee quote consumption (docs/plan/38 Task T5) — ANTI-BURN ordering:
	// AFTER the row exists but BEFORE any hold is posted, so a rejected
	// quote (expired/mismatch) never touches money — worst case cost is
	// one re-quote, never a stranded hold. txType "withdraw_settle"/gateway
	// "" matches settle()'s own ResolveFee call below exactly (the platform
	// fee doesn't vary by bank rail, docs/plan/25 Task T2).
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

	if err := m.submit(ctx, id); err != nil {
		// Hold already succeeded — money is safely parked in the user's
		// hold account. A submit failure here is not a Create-level
		// error; the resume job (step 3) retries Submit, which is
		// idempotent by request ID.
		m.logger.Error("payout: initial submit failed, resume job will retry",
			slog.Any("error", err), slog.String("request_id", id.String()))
	}

	return id, nil
}

// hold posts withdraw_initiate and records hold_tx_id + created->held.
// Safe to call again for the same request (crash-mid-flight resume,
// docs/plan/23 Task T6) — ledger.Post's own idempotency key is the money-
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

// maxFailoverAttempts bounds the failover loop in submit() (docs/plan/40
// Task T3 step 4) — belt-and-braces only: the routing exclusion list
// already guarantees termination in at most len(candidates) iterations
// (each rejected attempt permanently excludes one more vendor from a
// finite routing-rule set), so this cap only matters if that invariant is
// ever broken by a bug. Comfortably above any realistic candidate count.
const maxFailoverAttempts = 20

// classifySubmitOutcome maps one Submit round-trip to the outcome
// classification the anti-double-payout failover rule (mayFailover) reads
// from payout_vendor_calls (docs/plan/40 Task T3). callErr non-nil is a
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

// mayFailover implements docs/plan/40's locked anti-double-payout rule
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

// submit moves held/vendor_pending -> submitted and calls the vendor,
// failing over to the next routing candidate on a definitive synchronous
// rejection (docs/plan/40 Task T3) as long as mayFailover allows it.
// Called from Create (fresh request), the resume job (retrying a stuck
// 'submitted' request — Submit is idempotent by request ID, so re-calling
// it after an infra failure is always safe), and admin retry (Task T5).
func (m *Module) submit(ctx context.Context, id uuid.UUID) error {
	req, err := m.repo.Get(ctx, id)
	if err != nil {
		return err
	}

	// TransitionToSubmitted is a no-op (won=false) if the request is
	// already terminal or mid-flight elsewhere — proceed with Submit
	// regardless (it's idempotent by request ID either way), but skip if
	// we're clearly not in a submittable state at all.
	if req.Status != model.StatusHeld && req.Status != model.StatusSubmitted && req.Status != model.StatusVendorPending {
		return fmt.Errorf("%w: cannot submit request in status %q", ErrInvalidTransition, req.Status)
	}
	if _, err := m.repo.TransitionToSubmitted(ctx, id); err != nil {
		return fmt.Errorf("transition to submitted: %w", err)
	}

	vendor := req.Vendor
	tried := make([]string, 0, 1)

	for attempt := 0; attempt < maxFailoverAttempts; attempt++ {
		provider, ok := m.registry.Payout(vendor)
		if !ok {
			return fmt.Errorf("%w: %s", ErrUnknownVendor, vendor)
		}

		result, submitErr := provider.Submit(ctx, id.String(), req.Amount, req.Currency, req.Destination)
		outcome := classifySubmitOutcome(result, submitErr)
		m.recordVendorCall(ctx, id, fmt.Sprintf("submit amount=%s currency=%s vendor=%s", req.Amount, req.Currency, vendor), result, submitErr, outcome)
		if m.breaker != nil {
			if submitErr != nil {
				m.breaker.RecordFailure(vendor)
			} else {
				// Includes a business rejection (PayoutFailed with no
				// error) — the vendor WAS reachable, gotcha #13 master.
				m.breaker.RecordSuccess(vendor)
			}
		}

		if submitErr != nil {
			// Uncertain — status stays 'submitted', PINNED to this vendor;
			// the resume job retries the SAME vendor (Submit is idempotent
			// by request ID), never a new one.
			if setErr := m.repo.SetError(ctx, id, submitErr.Error()); setErr != nil {
				m.logger.Error("payout: set error failed", slog.Any("error", setErr), slog.String("request_id", id.String()))
			}
			return submitErr
		}

		if result.Status == vendorgw.PayoutFailed {
			tried = append(tried, vendor)
			calls, callsErr := m.repo.ListVendorCalls(ctx, id)
			if callsErr != nil {
				m.logger.Error("payout: list vendor calls failed, refusing failover", slog.Any("error", callsErr), slog.String("request_id", id.String()))
			}
			if callsErr == nil && mayFailover(calls) {
				nextVendor, _, routeErr := m.ResolvePayoutRoute(ctx, req.UserID, req.Currency, req.Amount, tried)
				if routeErr == nil {
					vendor = nextVendor
					if setErr := m.repo.SetVendor(ctx, id, vendor); setErr != nil {
						m.logger.Error("payout: set vendor failed", slog.Any("error", setErr), slog.String("request_id", id.String()))
					}
					continue
				}
				// ErrNoRoute/ErrNoVendorAvailable — no more candidates, fall
				// through to cancel below.
			}
			gateway, gErr := m.gatewayForVendor(ctx, vendor)
			if gErr != nil {
				return gErr
			}
			return m.cancel(ctx, id, gateway, result.Reason)
		}

		gateway, gErr := m.gatewayForVendor(ctx, vendor)
		if gErr != nil {
			return gErr
		}
		switch result.Status {
		case vendorgw.PayoutSettled:
			return m.settle(ctx, id, gateway)
		case vendorgw.PayoutPending:
			if _, err := m.repo.TransitionToVendorPending(ctx, id, result.VendorRef); err != nil {
				return fmt.Errorf("transition to vendor_pending: %w", err)
			}
			return nil
		default:
			return fmt.Errorf("payout: unknown vendor result status %q", result.Status)
		}
	}
	return fmt.Errorf("payout: exhausted failover attempts for request %s", id)
}

// settle posts withdraw_settle (docs/plan/23 Task T4) and moves the
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
	// transaction type/currency (docs/plan/25 Task T2).
	//
	// docs/plan/38 Task T5: a request created WITH a fee quote already has
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

// cancel posts withdraw_cancel (docs/plan/23 Task T4) and moves the
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

// reconcileAfterLostRace handles ledgererr.ErrAlreadyClosed (docs/plan/23
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

func (m *Module) recordVendorCall(ctx context.Context, requestID uuid.UUID, summary string, result vendorgw.PayoutResult, callErr error, outcome string) {
	call := model.PayoutVendorCall{
		ID: generalutil.NewV7(), PayoutRequestID: requestID, Attempt: 1, ReqSummary: summary, Outcome: outcome,
	}
	if callErr != nil {
		call.Error = callErr.Error()
	} else {
		call.RespStatus = string(result.Status)
	}
	if err := m.repo.InsertVendorCall(ctx, call); err != nil {
		m.logger.Error("payout: record vendor call failed", slog.Any("error", err), slog.String("request_id", requestID.String()))
	}
}

// ResumeStuck re-drives requests that crashed or stalled mid-flight
// (docs/plan/23 Task T3 step 3 — "jawaban crash-mid-flight: state machine +
// job resume, bukan saga framework"), covering EVERY non-terminal status a
// crash can leave a request in, not just 'submitted'/'vendor_pending' — a
// crash right after 'created' (before hold() ever ran) or right after
// 'held' (money already parked in the hold account, but before
// TransitionToSubmitted) would otherwise leave a request permanently
// orphaned with no resume path (docs/plan/23 Task T6's own chaos scenario
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
		if err := m.submit(ctx, req.ID); err != nil {
			m.logger.Error("payout-resume: retry submit (from created) failed",
				slog.Any("error", err), slog.String("request_id", req.ID.String()))
			failed++
			continue
		}
		resumed++
	}

	for _, status := range []string{model.StatusHeld, model.StatusSubmitted} {
		stuck, listErr := m.repo.ListStuck(ctx, status, cutoff, 100)
		if listErr != nil {
			return resumed, failed, fmt.Errorf("payout: resume: list stuck %s: %w", status, listErr)
		}
		for _, req := range stuck {
			if err := m.submit(ctx, req.ID); err != nil {
				m.logger.Error("payout-resume: retry submit failed",
					slog.Any("error", err), slog.String("request_id", req.ID.String()))
				failed++
				continue
			}
			resumed++
		}
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
	m.recordVendorCall(ctx, req.ID, fmt.Sprintf("query vendor=%s vendor_ref=%s", req.Vendor, req.VendorRef), result, queryErr, classifyQueryOutcome(queryErr))
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

// AdminCancel cancels a stuck request via an admin action (docs/plan/23
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

// AdminRetry re-submits a request stuck in 'submitted' (docs/plan/23 Task
// T5) — identical to what the resume job itself does on its next pass,
// exposed as a manual action for an operator who doesn't want to wait.
func (m *Module) AdminRetry(ctx context.Context, id uuid.UUID) error {
	req, err := m.repo.Get(ctx, id)
	if err != nil {
		return err
	}
	if req.Status != model.StatusSubmitted {
		return fmt.Errorf("%w: cannot retry request in status %q", ErrInvalidTransition, req.Status)
	}
	return m.submit(ctx, id)
}

// Get returns one payout request by id.
func (m *Module) Get(ctx context.Context, id uuid.UUID) (PayoutRequest, error) {
	return m.repo.Get(ctx, id)
}

// List returns payout requests newest first (docs/plan/23 Task T5 admin
// endpoint). status/vendor empty = no filter.
func (m *Module) List(ctx context.Context, status, vendor string, limit, offset int) ([]PayoutRequest, error) {
	return m.repo.List(ctx, status, vendor, limit, offset)
}
