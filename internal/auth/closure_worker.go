package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/auth/model"
	"github.com/herdifirdausss/seev/pkg/cryptox"
)

const (
	closurePollInterval = 15 * time.Second
	// closureMaxRetries caps a transient owner-call failure's retry budget
	// before the request is marked 'dead' (K10 work item 1's own "dead
	// status") — an operator must investigate and replay, same posture as
	// the outbox relay's dead-lettering.
	closureMaxRetries  = 5
	closureBaseBackoff = 30 * time.Second
)

// StartClosureWorker wires and starts docs/roadmap/archive/51-a8-data-lifecycle-privacy.md
// T5's (K10) closure saga driver. Returns (nil, nil) when no closure ring
// or registered owner is configured — matches StartPrivacyExportWorker's
// own "storage/dependencies are optional in this binary" convention.
func (m *Module) StartClosureWorker(ctx context.Context, logger *slog.Logger) (stop func(), err error) {
	if m.closureRing == nil || len(m.closureOwners) == 0 {
		return nil, nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(closurePollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := m.ProcessOnePendingClosure(ctx); err != nil {
					logger.Error("closure saga: step failed", "error", err)
				}
				m.refreshPrivacyRequestsGauge(ctx, logger)
			}
		}
	}()
	return func() { cancel(); <-done }, nil
}

// ProcessOnePendingClosure claims exactly one due closure request (FOR
// UPDATE SKIP LOCKED — safe under concurrent replicas) and drives it
// forward exactly ONE step, matching its current status: pending ->
// preparing -> committing -> completed (or blocked/dead, both terminal).
// Small, resumable units of work — the same discipline
// AssembleOnePendingExport and payout's dispatchOne already use — mean a
// crash at any point simply leaves the row at its last durably-written
// status, and the next poll (or another replica) picks up exactly there;
// no step is ever silently skipped or re-run past what its own idempotent
// commit already guarantees. Exported for deterministic integration
// testing, same reasoning as AssembleOnePendingExport.
func (m *Module) ProcessOnePendingClosure(ctx context.Context) error {
	var id, userID, surrogateID uuid.UUID
	var status string
	claimed := false
	err := m.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, `
			SELECT id, user_id, status, surrogate_id
			FROM privacy_requests
			WHERE request_type = 'closure' AND status IN ('pending', 'preparing', 'committing')
			  AND (next_attempt_at IS NULL OR next_attempt_at <= now())
			ORDER BY requested_at
			LIMIT 1
			FOR UPDATE SKIP LOCKED`).Scan(&id, &userID, &status, &surrogateID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		claimed = true
		return nil
	})
	if err != nil || !claimed {
		return err
	}

	switch status {
	case "pending":
		return m.closureStepPrepare(ctx, id, userID)
	case "preparing":
		return m.closureStepCommit(ctx, id, userID, surrogateID)
	case "committing":
		return m.closureStepFinalize(ctx, id, userID)
	default:
		return nil
	}
}

// closureStepPrepare runs auth's own local blocking checks (K10: "a
// pending... KYC retry", "an active retention hold" — both auth-owned
// tables, no other-service call needed) first — cheapest check, and
// skips every owner call entirely if it already blocks — then loops
// every REGISTERED owner's own Prepare (A8 T5b: ledger, payin, payout,
// fraud, gateway — admin-bff/assurance are never registered, K11's own
// "when applicable"/"no hidden subject field" wording means neither owns
// end-user data to gate or move). Owners already checkpointed 'prepared'
// from a prior (possibly crashed) attempt are skipped — resumable at
// owner granularity within this one step, matching K11's own
// "owner_checkpoints... resumes forward from the last durable owner
// state". Every owner is still probed even after one reports blocked, so
// a single 'blocked' result names every real blocker at once rather than
// making the user retry once per owner.
func (m *Module) closureStepPrepare(ctx context.Context, id, userID uuid.UUID) error {
	blocked, reasons, err := m.localClosureBlockers(ctx, userID)
	if err != nil {
		return m.closureRetryOrDead(ctx, id, fmt.Errorf("local blocker check: %w", err))
	}
	if !blocked {
		checkpoints, err := m.loadClosureCheckpoints(ctx, id)
		if err != nil {
			return err
		}
		for _, owner := range m.closureOwners {
			if closureOwnerPhase(checkpoints, owner.name) == "prepared" {
				continue
			}
			ownerBlocked, ownerReasons, err := owner.client.Prepare(ctx, userID)
			if err != nil {
				privacyOwnerCallsTotal.WithLabelValues(owner.name, "prepare", "error").Inc()
				return m.closureRetryOrDead(ctx, id, fmt.Errorf("%s prepare: %w", owner.name, err))
			}
			privacyOwnerCallsTotal.WithLabelValues(owner.name, "prepare", "ok").Inc()
			if ownerBlocked {
				blocked = true
				reasons = append(reasons, ownerReasons...)
				continue
			}
			if err := m.closureCheckpointOwner(ctx, id, owner.name, "prepared", nil); err != nil {
				return err
			}
		}
	}
	if blocked {
		var requestedAt time.Time
		err := m.db.QueryRowContext(ctx, `
			UPDATE privacy_requests SET status = 'blocked', last_error = $1, updated_at = now() WHERE id = $2
			RETURNING requested_at`,
			truncateErrorMessage(strings.Join(reasons, "; ")), id).Scan(&requestedAt)
		if err != nil {
			return err
		}
		observePrivacyRequestDuration("closure", "blocked", requestedAt)
		return nil
	}
	return m.closureAdvanceStatus(ctx, id, "preparing")
}

// closureStepCommit calls every registered owner's Commit — access was
// already disabled at RequestClosure time (auth_users.status='closing'),
// matching K10's "disable access first, commit owners" ordering. Skips
// owners already checkpointed 'committed', same resumable-per-owner
// discipline as closureStepPrepare.
func (m *Module) closureStepCommit(ctx context.Context, id, userID, surrogateID uuid.UUID) error {
	checkpoints, err := m.loadClosureCheckpoints(ctx, id)
	if err != nil {
		return err
	}
	for _, owner := range m.closureOwners {
		if closureOwnerPhase(checkpoints, owner.name) == "committed" {
			continue
		}
		resultHash, affectedCount, err := owner.client.Commit(ctx, userID, surrogateID)
		if err != nil {
			privacyOwnerCallsTotal.WithLabelValues(owner.name, "commit", "error").Inc()
			return m.closureRetryOrDead(ctx, id, fmt.Errorf("%s commit: %w", owner.name, err))
		}
		privacyOwnerCallsTotal.WithLabelValues(owner.name, "commit", "ok").Inc()
		if err := m.closureCheckpointOwner(ctx, id, owner.name, "committed", map[string]any{
			"result_hash": resultHash, "affected_count": affectedCount,
		}); err != nil {
			return err
		}
	}
	return m.closureAdvanceStatus(ctx, id, "committing")
}

// closureStepFinalize is auth finalizing LAST (K10): remove credentials,
// revoke tokens, tombstone the identity, destroy the active-saga
// ciphertext, and mark the request completed — all in one local
// transaction (unlike the owner steps above, this never crosses a
// network boundary, so there is no partial-finalize state to resume from).
func (m *Module) closureStepFinalize(ctx context.Context, id, userID uuid.UUID) error {
	var requestedAt time.Time
	err := m.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM auth_credentials WHERE user_id = $1`, userID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE auth_refresh_tokens SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL`, userID); err != nil {
			return err
		}
		// Tombstone: fixed, non-reversible values — never the original
		// email/name. Keyed by the request id (already unique) so the
		// tombstone email itself can never collide across users. Sealed
		// into ciphertext (and the lookup digest recomputed) the exact
		// same way UserRepository.CreateUser/UpdateFullName do — there is
		// no plaintext column left to fall back to since "A8 T2.5b"'s
		// contract migration, so this direct-SQL bypass has to do its own
		// encryption rather than relying on a repository helper.
		tombstoneEmail := fmt.Sprintf("closed+%s@deleted.invalid", id)
		emailCiphertext, err := m.cryptoxRing.Seal(cryptox.AAD{Service: "auth", Table: "auth_users", Column: "email", RowID: userID.String()}, []byte(tombstoneEmail))
		if err != nil {
			return fmt.Errorf("seal tombstone email: %w", err)
		}
		fullNameCiphertext, err := m.cryptoxRing.Seal(cryptox.AAD{Service: "auth", Table: "auth_users", Column: "full_name", RowID: userID.String()}, []byte("[deleted]"))
		if err != nil {
			return fmt.Errorf("seal tombstone full_name: %w", err)
		}
		emailDigest := m.cryptoxLookup.Digest(strings.ToLower(strings.TrimSpace(tombstoneEmail)))
		v := m.cryptoxRing.CurrentVersion()
		if _, err := tx.ExecContext(ctx, `
			UPDATE auth_users
			SET email_ciphertext = $1, email_key_version = $2, email_lookup_digest = $3,
			    full_name_ciphertext = $4, full_name_key_version = $5, status = $6, updated_at = now()
			WHERE id = $7`,
			emailCiphertext, v, emailDigest, fullNameCiphertext, v, model.StatusClosed, userID); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, `
			UPDATE privacy_requests
			SET status = 'completed', active_subject_ciphertext = NULL, ready_at = now(), updated_at = now()
			WHERE id = $1
			RETURNING requested_at`, id).Scan(&requestedAt)
	})
	if err != nil {
		return m.closureRetryOrDead(ctx, id, fmt.Errorf("auth finalize: %w", err))
	}
	observePrivacyRequestDuration("closure", "completed", requestedAt)
	return nil
}

// localClosureBlockers checks the two K10 blocking conditions that live in
// auth's own database (no other-owner call needed): a pending KYC
// submission, and an active retention hold scoped to this subject.
func (m *Module) localClosureBlockers(ctx context.Context, userID uuid.UUID) (bool, []string, error) {
	var reasons []string

	var pendingKYC int
	if err := m.db.QueryRowContext(ctx, `
		SELECT count(*) FROM kyc_submissions WHERE user_id = $1 AND status = 'pending'`, userID).Scan(&pendingKYC); err != nil {
		return false, nil, err
	}
	if pendingKYC > 0 {
		reasons = append(reasons, fmt.Sprintf("%d kyc submission(s) still pending", pendingKYC))
	}

	var activeHold int
	if err := m.db.QueryRowContext(ctx, `
		SELECT count(*) FROM auth_retention_holds
		WHERE status = 'active' AND scope = 'subject' AND scope_value = $1`, userID.String()).Scan(&activeHold); err != nil {
		return false, nil, err
	}
	if activeHold > 0 {
		reasons = append(reasons, fmt.Sprintf("%d active retention hold(s)", activeHold))
	}

	return len(reasons) > 0, reasons, nil
}

// loadClosureCheckpoints reads and decodes owner_checkpoints — the shared
// read half of the per-owner resumability closureStepPrepare/
// closureStepCommit both need before deciding which owners still need a
// call this step.
func (m *Module) loadClosureCheckpoints(ctx context.Context, id uuid.UUID) (map[string]any, error) {
	var raw []byte
	if err := m.db.QueryRowContext(ctx, `SELECT owner_checkpoints FROM privacy_requests WHERE id = $1`, id).Scan(&raw); err != nil {
		return nil, fmt.Errorf("closure: read checkpoints: %w", err)
	}
	checkpoints := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &checkpoints); err != nil {
			return nil, fmt.Errorf("closure: decode checkpoints: %w", err)
		}
	}
	return checkpoints, nil
}

// closureOwnerPhase safely extracts checkpoints[owner]["phase"], "" if
// the owner has no checkpoint yet.
func closureOwnerPhase(checkpoints map[string]any, owner string) string {
	entry, ok := checkpoints[owner].(map[string]any)
	if !ok {
		return ""
	}
	phase, _ := entry["phase"].(string)
	return phase
}

// closureCheckpointOwner merges ONE owner's checkpoint into the existing
// JSONB (K11: "owner_checkpoints... lets a resumed saga skip owners
// already durably committed") without touching the request's own status
// — closureStepPrepare/closureStepCommit call this once per owner inside
// their own loop, then advance status once via closureAdvanceStatus after
// every owner in the loop succeeds.
func (m *Module) closureCheckpointOwner(ctx context.Context, id uuid.UUID, owner, phase string, extra map[string]any) error {
	checkpoints, err := m.loadClosureCheckpoints(ctx, id)
	if err != nil {
		return err
	}
	entry := map[string]any{"phase": phase}
	maps.Copy(entry, extra)
	checkpoints[owner] = entry
	encoded, err := json.Marshal(checkpoints)
	if err != nil {
		return fmt.Errorf("closure: encode checkpoints: %w", err)
	}
	_, err = m.db.ExecContext(ctx, `UPDATE privacy_requests SET owner_checkpoints = $1, updated_at = now() WHERE id = $2`, encoded, id)
	return err
}

// closureAdvanceStatus moves a request to nextStatus once every
// registered owner has completed the phase this step was driving, and
// clears any prior retry/backoff state now that this step succeeded.
func (m *Module) closureAdvanceStatus(ctx context.Context, id uuid.UUID, nextStatus string) error {
	_, err := m.db.ExecContext(ctx, `
		UPDATE privacy_requests
		SET status = $1, retry_count = 0, next_attempt_at = NULL, last_error = NULL, updated_at = now()
		WHERE id = $2`, nextStatus, id)
	return err
}

// closureRetryOrDead records a transient step failure — backoff and retry
// up to closureMaxRetries, then 'dead' (K10 work item 1). The request's
// status column is deliberately left unchanged: a retried step re-attempts
// the exact same phase it failed in, and per-owner checkpoints already
// recorded (closureCheckpointOwner) mean already-succeeded owners are
// skipped on retry, never re-called.
func (m *Module) closureRetryOrDead(ctx context.Context, id uuid.UUID, stepErr error) error {
	var retryCount int
	if err := m.db.QueryRowContext(ctx, `SELECT retry_count FROM privacy_requests WHERE id = $1`, id).Scan(&retryCount); err != nil {
		return fmt.Errorf("closure retry: read retry_count: %w", err)
	}
	retryCount++
	msg := truncateErrorMessage(stepErr.Error())
	if retryCount >= closureMaxRetries {
		var requestedAt time.Time
		err := m.db.QueryRowContext(ctx, `
			UPDATE privacy_requests SET status = 'dead', retry_count = $1, last_error = $2, updated_at = now() WHERE id = $3
			RETURNING requested_at`,
			retryCount, msg, id).Scan(&requestedAt)
		if err != nil {
			return err
		}
		observePrivacyRequestDuration("closure", "dead", requestedAt)
		return nil
	}
	backoff := closureBaseBackoff * time.Duration(1<<uint(retryCount-1))
	_, err := m.db.ExecContext(ctx, `
		UPDATE privacy_requests SET retry_count = $1, next_attempt_at = $2, last_error = $3, updated_at = now() WHERE id = $4`,
		retryCount, time.Now().Add(backoff), msg, id)
	return err
}
