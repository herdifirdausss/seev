package repository

//go:generate mockgen -source=vendor_command_repository.go -destination=vendor_command_repository_mock.go -package=repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/payout/model"
	"github.com/herdifirdausss/seev/pkg/database"
	"github.com/herdifirdausss/seev/pkg/generalutil"
)

// ErrCommandNotFound is returned by an operation that expects a specific
// vendor command to exist in a specific status and finds none.
var ErrCommandNotFound = errors.New("payout: vendor command not found")

// VendorCommandRepository persists payout_vendor_commands (docs/roadmap/archive/45
// Task T0/K1) — a durable outbox of "dispatch this payout to this vendor"
// work items the relay (Task T1) claims and executes, mirroring
// internal/ledger/repository.OutboxRepository's claim/retry/reap/replay
// shape. This is deliberately a SEPARATE interface from Repository, not an
// addition to it — same split as RoutingRepository, so a caller that only
// needs request-state transitions never has to depend on the command
// outbox's surface, and vice versa.
type VendorCommandRepository interface {
	// EnqueueInitialSubmit is the ONLY entry point that moves a request from
	// held/vendor_pending to submitted (docs/roadmap/archive/45 K1) — it transitions
	// the request AND inserts the first command (attempt 1) in one
	// transaction, so a transition can never commit without its command and
	// a command can never exist without its transition. Returns
	// (false, nil) if the request wasn't in a submittable status (no-op,
	// not an error) — matches the existing Transition* methods' contract.
	EnqueueInitialSubmit(ctx context.Context, payoutRequestID uuid.UUID, vendor string) (bool, error)

	// CompleteAndEnqueueFailover atomically completes completingCommandID
	// (must currently be 'processing' — the relay's own claimed command),
	// compare-and-swaps payout_requests.vendor from fromVendor to toVendor,
	// and inserts the next-attempt command for toVendor — all in one
	// transaction. Returns (false, nil) without any side effect if
	// fromVendor no longer matches the request's current vendor (a
	// concurrent process already moved it — this call loses the race
	// harmlessly rather than clobbering the winner).
	CompleteAndEnqueueFailover(ctx context.Context, payoutRequestID, completingCommandID uuid.UUID, fromVendor, toVendor string, nextAttempt int) (bool, error)

	// EnsureSubmitCommand is the resume job's idempotent recovery
	// (docs/roadmap/archive/45 K1): if payoutRequestID has no live command
	// (pending/processing/failed), insert one for vendor at the next
	// attempt number. Safe under multi-replica concurrency — a concurrent
	// insert conflicting on the one-live-command partial unique index (or,
	// more rarely, the plain (payout_request_id, attempt) unique
	// constraint, if two replicas computed the same next-attempt number
	// before either committed) is treated as "already ensured", not an
	// error. Returns whether THIS call inserted the command.
	EnsureSubmitCommand(ctx context.Context, payoutRequestID uuid.UUID, vendor string) (bool, error)

	// ClaimPendingCommands atomically claims up to limit 'pending' commands
	// (oldest first), marking them 'processing' — safe to call concurrently
	// from multiple relay replicas via FOR UPDATE SKIP LOCKED.
	ClaimPendingCommands(ctx context.Context, limit int) ([]model.PayoutVendorCommand, error)
	// ClaimFailedCommandsForRetry is like ClaimPendingCommands but for
	// 'failed' commands whose backoff (next_attempt_at) has elapsed and
	// whose retry budget isn't exhausted.
	ClaimFailedCommandsForRetry(ctx context.Context, limit int) ([]model.PayoutVendorCommand, error)

	// CompleteCommand marks a claimed ('processing') command as terminally
	// completed (the vendor call's outcome was accepted/settled/pending —
	// no further dispatch needed for this command). Returns
	// ErrCommandNotFound if commandID isn't currently 'processing'.
	CompleteCommand(ctx context.Context, commandID uuid.UUID) error
	// FailCommand records a failed attempt on a claimed ('processing')
	// command, incrementing retry_count and scheduling next_attempt_at via
	// exponential backoff with jitter (same formula as the ledger outbox's
	// MarkFailed: base 30s, factor 2, cap 15m, +up to 50% jitter). Once
	// retry_count reaches max_retries the command becomes 'dead' in the
	// same statement. Returns ErrCommandNotFound if commandID isn't
	// currently 'processing'.
	FailCommand(ctx context.Context, commandID uuid.UUID, errMsg string) error
	// ReapStuckCommands resets commands that have been 'processing' for
	// longer than olderThan back to 'failed' for an immediate retry
	// (worker crashed/died between claim and mark). retry_count is
	// deliberately NOT incremented — a reap proves nothing about whether
	// the vendor call itself was ever attempted, let alone completed
	// (docs/roadmap/archive/45 K2). Returns the number of commands reaped.
	ReapStuckCommands(ctx context.Context, olderThan time.Duration) (int, error)

	// ReplayDeadCommand resets one 'dead' command back to 'failed' with a
	// clean retry budget so the relay's normal retry claim picks it up.
	// Returns ErrCommandNotFound if id doesn't exist or isn't 'dead'.
	ReplayDeadCommand(ctx context.Context, id uuid.UUID) error
	// ReplayAllDeadCommands replays every 'dead' command created before
	// olderThan, capped at maxReplayAllBatch per call (an admin operation
	// must never be able to flood the vendor with an unbounded replay
	// burst). Returns the number replayed.
	ReplayAllDeadCommands(ctx context.Context, olderThan time.Time) (int, error)
	// ListDeadCommands returns operator-visible dead commands without exposing
	// destination payloads or credentials.
	ListDeadCommands(ctx context.Context, limit, offset int) ([]model.PayoutVendorCommand, error)

	// CountCommandsByStatuses returns the number of commands in each
	// requested status in one query — feeds the payout_vendor_commands
	// gauge (docs/roadmap/archive/45 K6). A status with zero rows is absent from the
	// map; callers must treat a missing key as 0.
	CountCommandsByStatuses(ctx context.Context, statuses []string) (map[string]int, error)

	// GetLiveCommand returns the current live command
	// (pending/processing/failed) for a request, if any — used by the
	// resume job to decide whether EnsureSubmitCommand needs to run.
	GetLiveCommand(ctx context.Context, payoutRequestID uuid.UUID) (model.PayoutVendorCommand, bool, error)

	// HasDeadCommand reports whether the MOST RECENT command for a request
	// is 'dead' — docs/roadmap/archive/45 Task T1's resume job uses this to distinguish
	// "no live command and the last attempt dead-lettered" (must stay
	// visible to the operator, never silently auto-revived by the automatic
	// resume job) from every other no-live-command case (no command ever
	// existed, or the most recent one simply 'completed' — e.g. a request
	// somehow back in 'submitted' after an earlier attempt already
	// completed toward a different status), both of which are genuine gaps
	// worth an EnsureSubmitCommand recovery insert.
	HasDeadCommand(ctx context.Context, payoutRequestID uuid.UUID) (bool, error)

	// ListTriedVendors returns every distinct vendor a command has ever
	// been enqueued for on this request, oldest attempt first — the
	// failover exclusion list (docs/roadmap/archive/45 Task T1). Attempts (and
	// therefore "already tried" vendors) now span separate command rows
	// across separate relay dispatches instead of one in-process loop's
	// local slice, so this replaces that slice's role entirely.
	ListTriedVendors(ctx context.Context, payoutRequestID uuid.UUID) ([]string, error)
}

type commandRepo struct {
	db database.DatabaseSQL
}

func NewVendorCommandRepository(db database.DatabaseSQL) VendorCommandRepository {
	return &commandRepo{db: db}
}

// vendorCommandKey builds the internal dedup key (docs/roadmap/archive/45 K1) —
// "payout:<request_id>:submit:<attempt>". This is NOT the vendor-facing
// idempotency key (that stays payout_request_id itself, unchanged across
// retries) — it exists only so payout_vendor_commands.command_key can carry
// a UNIQUE constraint that makes a double-insert of the same attempt a
// loud conflict instead of a silent duplicate row.
func vendorCommandKey(payoutRequestID uuid.UUID, attempt int) string {
	return fmt.Sprintf("payout:%s:submit:%d", payoutRequestID, attempt)
}

func insertVendorCommand(ctx context.Context, tx *sql.Tx, payoutRequestID uuid.UUID, vendor string, attempt int) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO payout_vendor_commands
			(id, command_key, payout_request_id, vendor, attempt, status, next_attempt_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, 'pending', now(), now(), now())`,
		generalutil.NewV7(), vendorCommandKey(payoutRequestID, attempt), payoutRequestID, vendor, attempt,
	)
	if err != nil {
		return fmt.Errorf("insert vendor command: %w", err)
	}
	return nil
}

// insertVendorCommandIfAbsent is EnsureSubmitCommand's idempotent variant:
// ON CONFLICT DO NOTHING (no target — catches a conflict on ANY of the
// table's unique constraints/indexes: command_key, (payout_request_id,
// attempt), or the one-live-command partial index) turns "a command
// already exists" into a normal zero-rows-affected result instead of a
// statement error. This distinction matters: once ANY statement inside a
// Postgres transaction errors, the transaction is aborted and every
// subsequent statement (including Commit) fails until rollback — a plain
// INSERT whose Go-level duplicate-key error is merely swallowed by the
// caller would still leave the surrounding WithTx transaction unable to
// commit.
func insertVendorCommandIfAbsent(ctx context.Context, tx *sql.Tx, payoutRequestID uuid.UUID, vendor string, attempt int) (bool, error) {
	res, err := tx.ExecContext(ctx, `
		INSERT INTO payout_vendor_commands
			(id, command_key, payout_request_id, vendor, attempt, status, next_attempt_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, 'pending', now(), now(), now())
		ON CONFLICT DO NOTHING`,
		generalutil.NewV7(), vendorCommandKey(payoutRequestID, attempt), payoutRequestID, vendor, attempt,
	)
	if err != nil {
		return false, fmt.Errorf("insert vendor command if absent: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("insert vendor command if absent rows affected: %w", err)
	}
	return n > 0, nil
}

func (r *commandRepo) EnqueueInitialSubmit(ctx context.Context, payoutRequestID uuid.UUID, vendor string) (bool, error) {
	var won bool
	err := r.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
			UPDATE payout_requests SET status = 'submitted', updated_at = now()
			WHERE id = $1 AND status IN ('held', 'vendor_pending')`,
			payoutRequestID)
		if err != nil {
			return fmt.Errorf("enqueue initial submit: transition: %w", err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("enqueue initial submit: rows affected: %w", err)
		}
		if n == 0 {
			won = false
			return nil
		}
		if err := insertVendorCommand(ctx, tx, payoutRequestID, vendor, 1); err != nil {
			return fmt.Errorf("enqueue initial submit: %w", err)
		}
		won = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return won, nil
}

func (r *commandRepo) CompleteAndEnqueueFailover(ctx context.Context, payoutRequestID, completingCommandID uuid.UUID, fromVendor, toVendor string, nextAttempt int) (bool, error) {
	var won bool
	err := r.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
			UPDATE payout_requests SET vendor = $1, updated_at = now()
			WHERE id = $2 AND vendor = $3`,
			toVendor, payoutRequestID, fromVendor)
		if err != nil {
			return fmt.Errorf("complete and enqueue failover: cas vendor: %w", err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("complete and enqueue failover: cas vendor rows affected: %w", err)
		}
		if n == 0 {
			won = false
			return nil
		}

		res, err = tx.ExecContext(ctx, `
			UPDATE payout_vendor_commands SET status = 'completed', locked_at = NULL, updated_at = now()
			WHERE id = $1 AND status = 'processing'`,
			completingCommandID)
		if err != nil {
			return fmt.Errorf("complete and enqueue failover: complete command: %w", err)
		}
		n, err = res.RowsAffected()
		if err != nil {
			return fmt.Errorf("complete and enqueue failover: complete command rows affected: %w", err)
		}
		if n == 0 {
			return fmt.Errorf("%w: %s is not a live claimed command", ErrCommandNotFound, completingCommandID)
		}

		if err := insertVendorCommand(ctx, tx, payoutRequestID, toVendor, nextAttempt); err != nil {
			return fmt.Errorf("complete and enqueue failover: %w", err)
		}
		won = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return won, nil
}

func (r *commandRepo) EnsureSubmitCommand(ctx context.Context, payoutRequestID uuid.UUID, vendor string) (bool, error) {
	var inserted bool
	err := r.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		var nextAttempt int
		row := tx.QueryRowContext(ctx, `
			SELECT COALESCE(MAX(attempt), 0) + 1 FROM payout_vendor_commands WHERE payout_request_id = $1`,
			payoutRequestID)
		if err := row.Scan(&nextAttempt); err != nil {
			return fmt.Errorf("ensure submit command: resolve next attempt: %w", err)
		}
		ok, err := insertVendorCommandIfAbsent(ctx, tx, payoutRequestID, vendor, nextAttempt)
		if err != nil {
			return fmt.Errorf("ensure submit command: %w", err)
		}
		// ok=false means either the one-live-command partial unique index
		// (a live command already exists) or, more rarely, the plain
		// (payout_request_id, attempt) constraint (a concurrent replica
		// computed and committed the same attempt number first) absorbed a
		// conflict — either way a command now exists for this request,
		// which is exactly what this call wanted; not an error.
		inserted = ok
		return nil
	})
	if err != nil {
		return false, err
	}
	return inserted, nil
}

// commandColumns is reused both in a plain SELECT (GetLiveCommand) and
// inside a WITH ... RETURNING ... SELECT chain (claim) — it must therefore
// list bare column names only. An expression like COALESCE(last_error, ”)
// would work in the plain-SELECT case but break the RETURNING case: an
// unaliased expression in RETURNING gets an auto-generated name (e.g.
// "coalesce"), and the outer SELECT's own COALESCE(last_error, ”) then
// fails to resolve "last_error" as a column of the CTE. NULL handling for
// nullable columns lives in scanCommand instead.
const commandColumns = `id, command_key, payout_request_id, vendor, attempt, status, retry_count, max_retries,
	next_attempt_at, last_attempted_at, locked_at, last_error, created_at, updated_at`

func scanCommand(s rowScanner) (model.PayoutVendorCommand, error) {
	var c model.PayoutVendorCommand
	var nextAttemptAt, lastAttemptedAt, lockedAt sql.NullTime
	var lastError sql.NullString
	if err := s.Scan(&c.ID, &c.CommandKey, &c.PayoutRequestID, &c.Vendor, &c.Attempt, &c.Status,
		&c.RetryCount, &c.MaxRetries, &nextAttemptAt, &lastAttemptedAt, &lockedAt, &lastError,
		&c.CreatedAt, &c.UpdatedAt); err != nil {
		return model.PayoutVendorCommand{}, fmt.Errorf("scan vendor command: %w", err)
	}
	if nextAttemptAt.Valid {
		c.NextAttemptAt = &nextAttemptAt.Time
	}
	if lastAttemptedAt.Valid {
		c.LastAttemptedAt = &lastAttemptedAt.Time
	}
	if lockedAt.Valid {
		c.LockedAt = &lockedAt.Time
	}
	if lastError.Valid {
		c.LastError = lastError.String
	}
	return c, nil
}

func (r *commandRepo) claim(ctx context.Context, selectWhere, orderBy string, limit int) ([]model.PayoutVendorCommand, error) {
	rows, err := r.db.QueryContext(ctx, fmt.Sprintf(`
		WITH claimed AS (
			UPDATE payout_vendor_commands
			SET status = 'processing', last_attempted_at = now(), locked_at = now(), updated_at = now()
			WHERE id IN (
				SELECT id FROM payout_vendor_commands
				WHERE %s
				ORDER BY %s
				LIMIT $1
				FOR UPDATE SKIP LOCKED
			)
			RETURNING %s
		)
		SELECT %s FROM claimed`, selectWhere, orderBy, commandColumns, commandColumns),
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("claim vendor commands: %w", err)
	}
	defer rows.Close()

	out := make([]model.PayoutVendorCommand, 0)
	for rows.Next() {
		c, err := scanCommand(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *commandRepo) ClaimPendingCommands(ctx context.Context, limit int) ([]model.PayoutVendorCommand, error) {
	return r.claim(ctx, "status = 'pending'", "created_at ASC", limit)
}

func (r *commandRepo) ClaimFailedCommandsForRetry(ctx context.Context, limit int) ([]model.PayoutVendorCommand, error) {
	return r.claim(ctx,
		"status = 'failed' AND retry_count < max_retries AND (next_attempt_at IS NULL OR next_attempt_at <= now())",
		"next_attempt_at ASC NULLS FIRST", limit)
}

func (r *commandRepo) CompleteCommand(ctx context.Context, commandID uuid.UUID) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE payout_vendor_commands SET status = 'completed', locked_at = NULL, updated_at = now()
		WHERE id = $1 AND status = 'processing'`,
		commandID)
	if err != nil {
		return fmt.Errorf("complete vendor command: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("complete vendor command rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: %s", ErrCommandNotFound, commandID)
	}
	return nil
}

// FailCommand's backoff formula is identical to
// internal/ledger/repository/outbox_event_repository.go's MarkFailed: base
// 30s, factor 2, cap 15m, plus up to 50% jitter — kept in lockstep
// deliberately so the two outbox implementations don't drift into two
// different retry-timing philosophies for no reason.
func (r *commandRepo) FailCommand(ctx context.Context, commandID uuid.UUID, errMsg string) error {
	if len(errMsg) > 1000 {
		errMsg = errMsg[:1000]
	}
	res, err := r.db.ExecContext(ctx, `
		UPDATE payout_vendor_commands
		SET status = CASE WHEN retry_count + 1 >= max_retries THEN 'dead' ELSE 'failed' END,
		    retry_count = retry_count + 1,
		    last_error = $1,
		    last_attempted_at = now(),
		    locked_at = NULL,
		    updated_at = now(),
		    next_attempt_at = now() + (
		        LEAST(900, 30 * POWER(2, retry_count + 1)) * (1 + random() * 0.5)
		    ) * INTERVAL '1 second'
		WHERE id = $2 AND status = 'processing'`,
		errMsg, commandID)
	if err != nil {
		return fmt.Errorf("fail vendor command: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("fail vendor command rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: %s", ErrCommandNotFound, commandID)
	}
	return nil
}

func (r *commandRepo) ReapStuckCommands(ctx context.Context, olderThan time.Duration) (int, error) {
	res, err := r.db.ExecContext(ctx, `
		UPDATE payout_vendor_commands
		SET status = 'failed', next_attempt_at = now(), locked_at = NULL,
		    last_error = 'reaped: stuck in processing past lease deadline', updated_at = now()
		WHERE status = 'processing' AND locked_at < now() - $1::interval`,
		fmt.Sprintf("%d seconds", int(olderThan.Seconds())),
	)
	if err != nil {
		return 0, fmt.Errorf("reap stuck vendor commands: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("reap stuck vendor commands rows affected: %w", err)
	}
	return int(affected), nil
}

func (r *commandRepo) ReplayDeadCommand(ctx context.Context, id uuid.UUID) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE payout_vendor_commands
		SET status = 'failed', retry_count = 0, next_attempt_at = now(), locked_at = NULL,
		    last_error = COALESCE(last_error, '') || ' [replayed by admin]', updated_at = now()
		WHERE id = $1 AND status = 'dead'`,
		id)
	if err != nil {
		return fmt.Errorf("replay dead vendor command: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("replay dead vendor command rows affected: %w", err)
	}
	if affected == 0 {
		return fmt.Errorf("%w: %s", ErrCommandNotFound, id)
	}
	return nil
}

const maxReplayAllCommandsBatch = 100

func (r *commandRepo) ReplayAllDeadCommands(ctx context.Context, olderThan time.Time) (int, error) {
	res, err := r.db.ExecContext(ctx, `
		UPDATE payout_vendor_commands
		SET status = 'failed', retry_count = 0, next_attempt_at = now(), locked_at = NULL,
		    last_error = COALESCE(last_error, '') || ' [replayed by admin]', updated_at = now()
		WHERE id IN (
			SELECT id FROM payout_vendor_commands
			WHERE status = 'dead' AND created_at < $1
			ORDER BY created_at ASC
			LIMIT $2
			FOR UPDATE SKIP LOCKED
		)`,
		olderThan, maxReplayAllCommandsBatch,
	)
	if err != nil {
		return 0, fmt.Errorf("replay all dead vendor commands: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("replay all dead vendor commands rows affected: %w", err)
	}
	return int(affected), nil
}

func (r *commandRepo) ListDeadCommands(ctx context.Context, limit, offset int) ([]model.PayoutVendorCommand, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+commandColumns+`
		FROM payout_vendor_commands
		WHERE status = 'dead'
		ORDER BY created_at ASC, id ASC
		LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list dead vendor commands: %w", err)
	}
	defer rows.Close()
	commands := make([]model.PayoutVendorCommand, 0, limit)
	for rows.Next() {
		command, scanErr := scanCommand(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		commands = append(commands, command)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list dead vendor commands rows: %w", err)
	}
	return commands, nil
}

func (r *commandRepo) CountCommandsByStatuses(ctx context.Context, statuses []string) (map[string]int, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT status, COUNT(*) FROM payout_vendor_commands WHERE status = ANY($1) GROUP BY status`,
		statuses)
	if err != nil {
		return nil, fmt.Errorf("count vendor commands by statuses: %w", err)
	}
	defer rows.Close()

	counts := make(map[string]int, len(statuses))
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, fmt.Errorf("scan vendor command status count: %w", err)
		}
		counts[status] = count
	}
	return counts, rows.Err()
}

func (r *commandRepo) GetLiveCommand(ctx context.Context, payoutRequestID uuid.UUID) (model.PayoutVendorCommand, bool, error) {
	row := r.db.QueryRowContext(ctx, fmt.Sprintf(`
		SELECT %s FROM payout_vendor_commands
		WHERE payout_request_id = $1 AND status IN ('pending', 'processing', 'failed')
		LIMIT 1`, commandColumns),
		payoutRequestID)
	c, err := scanCommand(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.PayoutVendorCommand{}, false, nil
		}
		return model.PayoutVendorCommand{}, false, err
	}
	return c, true, nil
}

func (r *commandRepo) HasDeadCommand(ctx context.Context, payoutRequestID uuid.UUID) (bool, error) {
	var isDead bool
	err := r.db.QueryRowContext(ctx, `
		SELECT status = 'dead' FROM payout_vendor_commands
		WHERE payout_request_id = $1
		ORDER BY attempt DESC
		LIMIT 1`,
		payoutRequestID).Scan(&isDead)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("check most recent vendor command is dead: %w", err)
	}
	return isDead, nil
}

func (r *commandRepo) ListTriedVendors(ctx context.Context, payoutRequestID uuid.UUID) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT DISTINCT vendor FROM payout_vendor_commands WHERE payout_request_id = $1 ORDER BY vendor`,
		payoutRequestID)
	if err != nil {
		return nil, fmt.Errorf("list tried vendors: %w", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var vendor string
		if err := rows.Scan(&vendor); err != nil {
			return nil, fmt.Errorf("scan tried vendor: %w", err)
		}
		out = append(out, vendor)
	}
	return out, rows.Err()
}
