package payout

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/payout/model"
	"github.com/herdifirdausss/seev/internal/vendorgw"
)

// This file is the relay's domain logic (docs/roadmap/archive/45 Task T1/K2) — the
// ONLY place provider.Submit is ever called from. It lives inside the
// payout package (not internal/payout/worker) because dispatching a
// command needs settle/cancel/recordVendorCall/ResolvePayoutRoute, all
// unexported Module methods; internal/payout/worker only ever gets a thin
// ticker loop that calls back in through DispatchPendingCommands etc. (same
// split as internal/payout/worker.ResumeJob calling back through
// ResumeStuck/CountStuck).
//
// Delivery is honestly at-least-once (docs/roadmap/archive/45 K1) — a network timeout
// can never prove the vendor didn't receive the call, so a retried command
// always reuses the SAME vendor-facing idempotency key
// (payout_requests.id, unchanged across attempts) rather than claiming
// exactly-once.

// DispatchPendingCommands claims up to limit 'pending' commands and
// dispatches each one to its vendor. Returns the number claimed (not the
// number that succeeded — a claimed command that fails is retried later via
// its own backoff, not surfaced as an error here).
func (m *Module) DispatchPendingCommands(ctx context.Context, limit int) (int, error) {
	cmds, err := m.commandRepo.ClaimPendingCommands(ctx, limit)
	if err != nil {
		return 0, fmt.Errorf("payout-relay: claim pending commands: %w", err)
	}
	for _, cmd := range cmds {
		m.dispatchOne(ctx, cmd)
	}
	return len(cmds), nil
}

// DispatchFailedCommandsForRetry is DispatchPendingCommands' sibling for
// 'failed' commands whose backoff has elapsed.
func (m *Module) DispatchFailedCommandsForRetry(ctx context.Context, limit int) (int, error) {
	cmds, err := m.commandRepo.ClaimFailedCommandsForRetry(ctx, limit)
	if err != nil {
		return 0, fmt.Errorf("payout-relay: claim failed commands for retry: %w", err)
	}
	for _, cmd := range cmds {
		m.dispatchOne(ctx, cmd)
	}
	return len(cmds), nil
}

// ReapStuckCommands returns lease-expired 'processing' commands to 'failed'
// for an immediate retry — see VendorCommandRepository.ReapStuckCommands's
// own doc comment for why retry_count is never incremented here.
func (m *Module) ReapStuckCommands(ctx context.Context, olderThan time.Duration) (int, error) {
	n, err := m.commandRepo.ReapStuckCommands(ctx, olderThan)
	if err != nil {
		return 0, fmt.Errorf("payout-relay: reap stuck commands: %w", err)
	}
	return n, nil
}

// CountCommandsByStatuses feeds the payout_vendor_commands gauge
// (docs/roadmap/archive/45 K6).
func (m *Module) CountCommandsByStatuses(ctx context.Context, statuses []string) (map[string]int, error) {
	return m.commandRepo.CountCommandsByStatuses(ctx, statuses)
}

// dispatchOne executes ONE claimed ('processing') command: call the vendor,
// classify the outcome, record the audit trail, update the breaker, then
// route to a terminal transition, a same-vendor retry, or a failover —
// exactly the decision tree submit()'s in-process loop used to make per
// iteration, now driven by a durable command row instead of a Go for loop.
// Every exit path either completes, fails (schedules a retry), or
// atomically enqueues a failover for cmd — never leaves it 'processing'.
func (m *Module) dispatchOne(ctx context.Context, cmd model.PayoutVendorCommand) {
	req, err := m.repo.Get(ctx, cmd.PayoutRequestID)
	if err != nil {
		m.failCommandLogged(ctx, cmd.ID, fmt.Errorf("load payout request: %w", err))
		return
	}
	if req.Status != model.StatusSubmitted {
		// The request moved on (e.g. an admin cancel racing this command's
		// own claim) between enqueue and now — calling the vendor here
		// would be wasted work. This is purely an efficiency/log-noise
		// improvement: the ledger's own closed_by_tx_id guard (K3) is
		// always the real money-safety backstop regardless, exactly like
		// every other terminal-transition race in this package.
		m.completeCommandLogged(ctx, cmd.ID, "request no longer submitted")
		return
	}

	provider, ok := m.registry.Payout(cmd.Vendor)
	if !ok {
		m.failCommandLogged(ctx, cmd.ID, fmt.Errorf("%w: %s", ErrUnknownVendor, cmd.Vendor))
		return
	}

	result, submitErr := provider.Submit(ctx, req.ID.String(), req.Amount, req.Currency, req.Destination)
	outcome := classifySubmitOutcome(result, submitErr)
	vendorCommandAttemptsTotal.WithLabelValues(outcome).Inc()

	summary := fmt.Sprintf("submit amount=%s currency=%s vendor=%s attempt=%d", req.Amount, req.Currency, cmd.Vendor, cmd.Attempt)
	recordErr := m.recordVendorCall(ctx, req.ID, summary, result, submitErr, outcome)
	if m.breaker != nil {
		if submitErr != nil {
			m.breaker.RecordFailure(ctx, cmd.Vendor)
		} else {
			// Includes a business rejection (PayoutFailed, nil error) — the
			// vendor WAS reachable (gotcha #13 master).
			m.breaker.RecordSuccess(ctx, cmd.Vendor)
		}
	}
	if recordErr != nil {
		// The persisted call history is the safety boundary that prevents a
		// later retry from paying through a second vendor (docs/roadmap/archive/45
		// K2: "audit failure = fail-closed"). Leave the request pinned,
		// retry this same command later — no terminal transition, no
		// failover, until the audit write itself succeeds.
		m.logger.Error("payout-relay: persist vendor call outcome failed, retrying command",
			slog.Any("error", recordErr), slog.String("command_id", cmd.ID.String()))
		m.failCommandLogged(ctx, cmd.ID, recordErr)
		return
	}

	switch outcome {
	case model.VendorCallUncertain:
		// Pinned to this vendor forever (mayFailover) — retry the SAME
		// command (same vendor, same idempotency key = req.ID) later.
		if setErr := m.repo.SetError(ctx, req.ID, submitErr.Error()); setErr != nil {
			m.logger.Error("payout-relay: set error failed", slog.Any("error", setErr), slog.String("request_id", req.ID.String()))
		}
		m.failCommandLogged(ctx, cmd.ID, submitErr)
	case model.VendorCallRejected:
		m.handleRejected(ctx, cmd, req, result)
	default: // accepted (settled or pending)
		m.handleAccepted(ctx, cmd, req, result)
	}
}

// handleAccepted routes a reachable, non-rejected vendor result to its
// terminal (settled) or intermediate (vendor_pending) transition, then
// completes the command. If the LOCAL transition itself fails (e.g. a
// transient ledger error on settle), the command is retried — re-dispatch
// re-calls the vendor too, but that is safe and expected: Submit/Query are
// idempotent by request ID, so a redundant vendor call after an already-
// accepted outcome returns the same result again (identical to how the
// pre-outbox synchronous submit() tolerated a settle failure by simply
// being re-driven, vendor call included, on the next resume tick).
func (m *Module) handleAccepted(ctx context.Context, cmd model.PayoutVendorCommand, req model.PayoutRequest, result vendorgw.PayoutResult) {
	gateway, gErr := m.gatewayForVendor(ctx, cmd.Vendor)
	if gErr != nil {
		m.failCommandLogged(ctx, cmd.ID, gErr)
		return
	}

	switch result.Status {
	case vendorgw.PayoutSettled:
		if err := m.settle(ctx, req.ID, gateway); err != nil {
			m.failCommandLogged(ctx, cmd.ID, err)
			return
		}
	case vendorgw.PayoutPending:
		if _, err := m.repo.TransitionToVendorPending(ctx, req.ID, result.VendorRef); err != nil {
			m.failCommandLogged(ctx, cmd.ID, err)
			return
		}
	default:
		m.failCommandLogged(ctx, cmd.ID, fmt.Errorf("payout: unknown vendor result status %q", result.Status))
		return
	}

	if err := m.commandRepo.CompleteCommand(ctx, cmd.ID); err != nil {
		m.logger.Error("payout-relay: complete command failed after successful dispatch",
			slog.Any("error", err), slog.String("command_id", cmd.ID.String()))
	}
}

// handleRejected mirrors submit()'s old failover branch: a definitive
// synchronous business rejection may fail over to the next routing
// candidate, as long as mayFailover still allows it (no call for this
// request has EVER landed accepted/uncertain — doc 40's anti-double-payout
// rule, unchanged).
func (m *Module) handleRejected(ctx context.Context, cmd model.PayoutVendorCommand, req model.PayoutRequest, result vendorgw.PayoutResult) {
	calls, err := m.repo.ListVendorCalls(ctx, req.ID)
	if err != nil {
		m.failCommandLogged(ctx, cmd.ID, fmt.Errorf("list vendor calls before failover: %w", err))
		return
	}
	if !mayFailover(calls) {
		// A concurrent command already recorded an accepted or uncertain
		// outcome and owns completion — this command's own role ends here,
		// same as submit()'s old "lost the race, return nil" branch.
		m.completeCommandLogged(ctx, cmd.ID, "lost failover race")
		return
	}

	tried, err := m.commandRepo.ListTriedVendors(ctx, req.ID)
	if err != nil {
		m.failCommandLogged(ctx, cmd.ID, fmt.Errorf("list tried vendors: %w", err))
		return
	}
	nextVendor, _, routeErr := m.ResolvePayoutRoute(ctx, req.UserID, req.Currency, req.Amount, tried)
	if routeErr == nil {
		won, err := m.commandRepo.CompleteAndEnqueueFailover(ctx, req.ID, cmd.ID, cmd.Vendor, nextVendor, cmd.Attempt+1)
		if err != nil {
			m.failCommandLogged(ctx, cmd.ID, fmt.Errorf("enqueue failover: %w", err))
			return
		}
		if !won {
			// The request's vendor no longer matched cmd.Vendor when the CAS
			// ran — only possible if something outside this single-live-
			// command invariant changed it concurrently. Defensive, not
			// expected in normal operation.
			m.logger.Warn("payout-relay: failover CAS lost, request vendor already changed",
				slog.String("request_id", req.ID.String()), slog.String("expected_vendor", cmd.Vendor))
			m.completeCommandLogged(ctx, cmd.ID, "failover CAS lost")
		}
		return
	}
	if !errors.Is(routeErr, ErrNoRoute) && !errors.Is(routeErr, ErrNoVendorAvailable) {
		m.failCommandLogged(ctx, cmd.ID, fmt.Errorf("resolve failover route: %w", routeErr))
		return
	}

	// No safe candidate remains after only definitive rejections — return
	// the hold.
	gateway, gErr := m.gatewayForVendor(ctx, cmd.Vendor)
	if gErr != nil {
		m.failCommandLogged(ctx, cmd.ID, gErr)
		return
	}
	if err := m.cancel(ctx, req.ID, gateway, result.Reason); err != nil {
		m.failCommandLogged(ctx, cmd.ID, err)
		return
	}
	m.completeCommandLogged(ctx, cmd.ID, "")
}

func (m *Module) failCommandLogged(ctx context.Context, commandID uuid.UUID, cause error) {
	if err := m.commandRepo.FailCommand(ctx, commandID, cause.Error()); err != nil {
		m.logger.Error("payout-relay: fail command failed", slog.Any("error", err), slog.String("command_id", commandID.String()))
	}
}

func (m *Module) completeCommandLogged(ctx context.Context, commandID uuid.UUID, reason string) {
	if err := m.commandRepo.CompleteCommand(ctx, commandID); err != nil {
		m.logger.Error("payout-relay: complete command failed", slog.Any("error", err),
			slog.String("command_id", commandID.String()), slog.String("reason", reason))
	}
}
