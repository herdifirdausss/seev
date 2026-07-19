//go:build integration

package repository_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/internal/payout/model"
	"github.com/herdifirdausss/seev/internal/payout/repository"
)

// TestEnqueueInitialSubmit_TransitionsAndInsertsCommandAtomically is
// docs/plan/45 Task T0's required rollback-invariant proof, positive half:
// a successful call moves held->submitted AND inserts exactly one 'pending'
// attempt-1 command, together.
func TestEnqueueInitialSubmit_TransitionsAndInsertsCommandAtomically(t *testing.T) {
	db := setupTestDB(t)
	repo := repository.NewRepository(db)
	cmdRepo := repository.NewVendorCommandRepository(db)
	ctx := context.Background()

	req := newTestRequest()
	require.NoError(t, repo.Insert(ctx, req))
	won, err := repo.TransitionToHeld(ctx, req.ID, uuid.New())
	require.NoError(t, err)
	require.True(t, won)

	won, err = cmdRepo.EnqueueInitialSubmit(ctx, req.ID, req.Vendor)
	require.NoError(t, err)
	assert.True(t, won)

	final, err := repo.Get(ctx, req.ID)
	require.NoError(t, err)
	assert.Equal(t, model.StatusSubmitted, final.Status)

	cmd, ok, err := cmdRepo.GetLiveCommand(ctx, req.ID)
	require.NoError(t, err)
	require.True(t, ok, "expected exactly one live command after EnqueueInitialSubmit")
	assert.Equal(t, model.CommandPending, cmd.Status)
	assert.Equal(t, 1, cmd.Attempt)
	assert.Equal(t, req.Vendor, cmd.Vendor)
}

// TestEnqueueInitialSubmit_WrongStatus_NoOp proves the transition guard
// still gates the whole atomic operation — a request not in held/
// vendor_pending gets no transition and no command, not a partial effect.
func TestEnqueueInitialSubmit_WrongStatus_NoOp(t *testing.T) {
	db := setupTestDB(t)
	repo := repository.NewRepository(db)
	cmdRepo := repository.NewVendorCommandRepository(db)
	ctx := context.Background()

	req := newTestRequest()
	require.NoError(t, repo.Insert(ctx, req)) // status: created, not held

	won, err := cmdRepo.EnqueueInitialSubmit(ctx, req.ID, req.Vendor)
	require.NoError(t, err)
	assert.False(t, won)

	final, err := repo.Get(ctx, req.ID)
	require.NoError(t, err)
	assert.Equal(t, model.StatusCreated, final.Status, "status must be untouched by a no-op enqueue")

	_, ok, err := cmdRepo.GetLiveCommand(ctx, req.ID)
	require.NoError(t, err)
	assert.False(t, ok, "no command must exist when the transition didn't happen")
}

// TestEnqueueInitialSubmit_RollsBackTransitionWhenCommandConflicts is
// docs/plan/45 Task T0's core required proof: "transition tidak boleh
// commit tanpa command dan command tidak boleh ada tanpa transition." A
// live command is pre-seeded out of band (bypassing EnqueueInitialSubmit)
// to force the one-live-command partial unique index to reject the second
// insert — proving that when the command insert fails, the ALREADY-
// EXECUTED status transition in the same transaction is rolled back too,
// not left half-applied.
func TestEnqueueInitialSubmit_RollsBackTransitionWhenCommandConflicts(t *testing.T) {
	db := setupTestDB(t)
	repo := repository.NewRepository(db)
	ctx := context.Background()

	req := newTestRequest()
	require.NoError(t, repo.Insert(ctx, req))
	won, err := repo.TransitionToHeld(ctx, req.ID, uuid.New())
	require.NoError(t, err)
	require.True(t, won)

	// Seed a live command directly, out of band, so EnqueueInitialSubmit's
	// own insert must collide with idx_payout_vendor_commands_one_live.
	_, err = db.ExecContext(ctx, `
		INSERT INTO payout_vendor_commands
			(id, command_key, payout_request_id, vendor, attempt, status, next_attempt_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, 1, 'pending', now(), now(), now())`,
		uuid.New(), "payout:"+req.ID.String()+":submit:1", req.ID, req.Vendor)
	require.NoError(t, err)

	cmdRepo := repository.NewVendorCommandRepository(db)
	_, err = cmdRepo.EnqueueInitialSubmit(ctx, req.ID, req.Vendor)
	require.Error(t, err, "the conflicting command insert must fail the whole transaction")

	final, err := repo.Get(ctx, req.ID)
	require.NoError(t, err)
	assert.Equal(t, model.StatusHeld, final.Status,
		"the status transition executed earlier in the SAME transaction must have rolled back with the failed insert")
}

func TestEnsureSubmitCommand_InsertsWhenNoneLive_NoOpWhenLiveExists(t *testing.T) {
	db := setupTestDB(t)
	repo := repository.NewRepository(db)
	cmdRepo := repository.NewVendorCommandRepository(db)
	ctx := context.Background()

	req := newTestRequest()
	require.NoError(t, repo.Insert(ctx, req))
	_, err := repo.TransitionToHeld(ctx, req.ID, uuid.New())
	require.NoError(t, err)

	inserted, err := cmdRepo.EnsureSubmitCommand(ctx, req.ID, req.Vendor)
	require.NoError(t, err)
	assert.True(t, inserted)

	cmd, ok, err := cmdRepo.GetLiveCommand(ctx, req.ID)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, 1, cmd.Attempt)

	// Second call: a live command already exists — must be a harmless no-op,
	// not a duplicate command and not an error.
	inserted, err = cmdRepo.EnsureSubmitCommand(ctx, req.ID, req.Vendor)
	require.NoError(t, err)
	assert.False(t, inserted)

	counts, err := cmdRepo.CountCommandsByStatuses(ctx, []string{model.CommandPending})
	require.NoError(t, err)
	assert.Equal(t, 1, counts[model.CommandPending], "exactly one live command must exist after both calls")
}

func TestCompleteAndEnqueueFailover_AtomicFailover(t *testing.T) {
	db := setupTestDB(t)
	repo := repository.NewRepository(db)
	cmdRepo := repository.NewVendorCommandRepository(db)
	ctx := context.Background()

	req := newTestRequest()
	require.NoError(t, repo.Insert(ctx, req))
	_, err := repo.TransitionToHeld(ctx, req.ID, uuid.New())
	require.NoError(t, err)
	won, err := cmdRepo.EnqueueInitialSubmit(ctx, req.ID, req.Vendor)
	require.NoError(t, err)
	require.True(t, won)

	claimed, err := cmdRepo.ClaimPendingCommands(ctx, 10)
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	firstCommand := claimed[0]

	const nextVendor = "mockvendor2"
	won, err = cmdRepo.CompleteAndEnqueueFailover(ctx, req.ID, firstCommand.ID, req.Vendor, nextVendor, 2)
	require.NoError(t, err)
	assert.True(t, won)

	final, err := repo.Get(ctx, req.ID)
	require.NoError(t, err)
	assert.Equal(t, nextVendor, final.Vendor)

	live, ok, err := cmdRepo.GetLiveCommand(ctx, req.ID)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, 2, live.Attempt)
	assert.Equal(t, nextVendor, live.Vendor)
	assert.Equal(t, model.CommandPending, live.Status)

	counts, err := cmdRepo.CountCommandsByStatuses(ctx, []string{model.CommandCompleted})
	require.NoError(t, err)
	assert.Equal(t, 1, counts[model.CommandCompleted], "the original claimed command must be completed")
}

// TestCompleteAndEnqueueFailover_VendorMismatch_NoOp proves the
// compare-and-swap guard: a concurrent process that already moved the
// request to a different vendor must make this call a harmless no-op,
// never a clobber of the winner.
func TestCompleteAndEnqueueFailover_VendorMismatch_NoOp(t *testing.T) {
	db := setupTestDB(t)
	repo := repository.NewRepository(db)
	cmdRepo := repository.NewVendorCommandRepository(db)
	ctx := context.Background()

	req := newTestRequest()
	require.NoError(t, repo.Insert(ctx, req))
	_, err := repo.TransitionToHeld(ctx, req.ID, uuid.New())
	require.NoError(t, err)
	_, err = cmdRepo.EnqueueInitialSubmit(ctx, req.ID, req.Vendor)
	require.NoError(t, err)
	claimed, err := cmdRepo.ClaimPendingCommands(ctx, 10)
	require.NoError(t, err)
	require.Len(t, claimed, 1)

	won, err := cmdRepo.CompleteAndEnqueueFailover(ctx, req.ID, claimed[0].ID, "not-the-current-vendor", "mockvendor2", 2)
	require.NoError(t, err)
	assert.False(t, won)

	final, err := repo.Get(ctx, req.ID)
	require.NoError(t, err)
	assert.Equal(t, req.Vendor, final.Vendor, "vendor must be unchanged when the CAS loses")

	live, ok, err := cmdRepo.GetLiveCommand(ctx, req.ID)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, 1, live.Attempt, "the original command must still be live, untouched")
	assert.Equal(t, model.CommandProcessing, live.Status)
}

// TestClaimPendingCommands_ConcurrentCallers_OneOwnerPerLease is the test
// matrix's required "dua relay claim batch bersamaan: satu owner per
// lease" proof for FOR UPDATE SKIP LOCKED.
func TestClaimPendingCommands_ConcurrentCallers_OneOwnerPerLease(t *testing.T) {
	db := setupTestDB(t)
	repo := repository.NewRepository(db)
	cmdRepo := repository.NewVendorCommandRepository(db)
	ctx := context.Background()

	const n = 10
	for i := 0; i < n; i++ {
		req := newTestRequest()
		require.NoError(t, repo.Insert(ctx, req))
		_, err := repo.TransitionToHeld(ctx, req.ID, uuid.New())
		require.NoError(t, err)
		_, err = cmdRepo.EnqueueInitialSubmit(ctx, req.ID, req.Vendor)
		require.NoError(t, err)
	}

	var wg sync.WaitGroup
	seen := make([][]uuid.UUID, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			claimed, err := cmdRepo.ClaimPendingCommands(ctx, n)
			assert.NoError(t, err)
			for _, c := range claimed {
				seen[idx] = append(seen[idx], c.ID)
			}
		}(i)
	}
	wg.Wait()

	total := 0
	uniq := make(map[uuid.UUID]int)
	for _, ids := range seen {
		total += len(ids)
		for _, id := range ids {
			uniq[id]++
		}
	}
	assert.Equal(t, n, total, "every pending command must be claimed exactly once across all callers")
	for id, count := range uniq {
		assert.Equal(t, 1, count, "command %s must have exactly one claimant", id)
	}
}

func TestFailCommand_BackoffThenDeadLetterThenReplay(t *testing.T) {
	db := setupTestDB(t)
	repo := repository.NewRepository(db)
	cmdRepo := repository.NewVendorCommandRepository(db)
	ctx := context.Background()

	req := newTestRequest()
	require.NoError(t, repo.Insert(ctx, req))
	_, err := repo.TransitionToHeld(ctx, req.ID, uuid.New())
	require.NoError(t, err)
	_, err = cmdRepo.EnqueueInitialSubmit(ctx, req.ID, req.Vendor)
	require.NoError(t, err)

	// max_retries defaults to 8 — fail it 8 times via claim+fail; the 8th
	// fail must flip it to 'dead' in the same statement. Backoff would
	// otherwise push next_attempt_at up to ~15m into the future — collapse
	// it back to now() between iterations so the test doesn't need to
	// sleep out FailCommand's real exponential-backoff+jitter schedule
	// (that schedule itself is covered separately by
	// TestClaimFailedCommandsForRetry_RespectsBackoff).
	var lastID uuid.UUID
	for i := 0; i < 8; i++ {
		claimed, cErr := cmdRepo.ClaimPendingCommands(ctx, 1)
		if len(claimed) == 0 {
			claimed, cErr = cmdRepo.ClaimFailedCommandsForRetry(ctx, 1)
		}
		require.NoError(t, cErr)
		require.Len(t, claimed, 1, "attempt %d", i+1)
		lastID = claimed[0].ID
		require.NoError(t, cmdRepo.FailCommand(ctx, lastID, "vendor timeout"))
		_, err = db.ExecContext(ctx, `UPDATE payout_vendor_commands SET next_attempt_at = now() WHERE id = $1`, lastID)
		require.NoError(t, err)
	}

	counts, err := cmdRepo.CountCommandsByStatuses(ctx, []string{model.CommandDead, model.CommandFailed})
	require.NoError(t, err)
	assert.Equal(t, 1, counts[model.CommandDead], "8th failure must dead-letter the command")
	assert.Equal(t, 0, counts[model.CommandFailed])

	// Replay must be visible (back to 'failed', retry budget reset) and
	// admin-batch-limited — proven separately by maxReplayAllCommandsBatch's
	// existence; here we prove the single-id replay path.
	require.NoError(t, cmdRepo.ReplayDeadCommand(ctx, lastID))
	live, ok, err := cmdRepo.GetLiveCommand(ctx, req.ID)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, model.CommandFailed, live.Status)
	assert.Equal(t, 0, live.RetryCount)

	err = cmdRepo.ReplayDeadCommand(ctx, uuid.New())
	assert.ErrorIs(t, err, repository.ErrCommandNotFound)
}

// TestReapStuckCommands proves a lease-expired 'processing' command is
// returned to 'failed' for an immediate retry WITHOUT incrementing
// retry_count (docs/plan/45 K2 — a reap proves nothing about whether the
// vendor call itself completed).
func TestReapStuckCommands(t *testing.T) {
	db := setupTestDB(t)
	repo := repository.NewRepository(db)
	cmdRepo := repository.NewVendorCommandRepository(db)
	ctx := context.Background()

	req := newTestRequest()
	require.NoError(t, repo.Insert(ctx, req))
	_, err := repo.TransitionToHeld(ctx, req.ID, uuid.New())
	require.NoError(t, err)
	_, err = cmdRepo.EnqueueInitialSubmit(ctx, req.ID, req.Vendor)
	require.NoError(t, err)
	claimed, err := cmdRepo.ClaimPendingCommands(ctx, 1)
	require.NoError(t, err)
	require.Len(t, claimed, 1)

	// Backdate locked_at directly so it looks like a lease that expired
	// long ago, without waiting out a real deadline in the test.
	_, err = db.ExecContext(ctx, `UPDATE payout_vendor_commands SET locked_at = now() - interval '1 hour' WHERE id = $1`, claimed[0].ID)
	require.NoError(t, err)

	n, err := cmdRepo.ReapStuckCommands(ctx, time.Minute)
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	live, ok, err := cmdRepo.GetLiveCommand(ctx, req.ID)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, model.CommandFailed, live.Status)
	assert.Equal(t, 0, live.RetryCount, "reap must not consume retry budget")
	assert.Nil(t, live.LockedAt)
}

// TestClaimFailedCommandsForRetry_RespectsBackoff proves next_attempt_at
// gates eligibility — a command whose backoff hasn't elapsed yet must not
// be claimable, even though it's already 'failed'.
func TestClaimFailedCommandsForRetry_RespectsBackoff(t *testing.T) {
	db := setupTestDB(t)
	repo := repository.NewRepository(db)
	cmdRepo := repository.NewVendorCommandRepository(db)
	ctx := context.Background()

	req := newTestRequest()
	require.NoError(t, repo.Insert(ctx, req))
	_, err := repo.TransitionToHeld(ctx, req.ID, uuid.New())
	require.NoError(t, err)
	_, err = cmdRepo.EnqueueInitialSubmit(ctx, req.ID, req.Vendor)
	require.NoError(t, err)
	claimed, err := cmdRepo.ClaimPendingCommands(ctx, 1)
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	require.NoError(t, cmdRepo.FailCommand(ctx, claimed[0].ID, "transient error"))

	retryable, err := cmdRepo.ClaimFailedCommandsForRetry(ctx, 10)
	require.NoError(t, err)
	assert.Empty(t, retryable, "a fresh backoff window must not be immediately retryable")

	_, err = db.ExecContext(ctx, `UPDATE payout_vendor_commands SET next_attempt_at = now() - interval '1 second' WHERE payout_request_id = $1`, req.ID)
	require.NoError(t, err)

	retryable, err = cmdRepo.ClaimFailedCommandsForRetry(ctx, 10)
	require.NoError(t, err)
	assert.Len(t, retryable, 1, "an elapsed backoff window must become claimable")
}

// TestConcurrentEnsureSubmitCommand_ExactlyOneWins is the multi-replica
// safety proof for EnsureSubmitCommand's own conflict handling.
func TestConcurrentEnsureSubmitCommand_ExactlyOneWins(t *testing.T) {
	db := setupTestDB(t)
	repo := repository.NewRepository(db)
	cmdRepo := repository.NewVendorCommandRepository(db)
	ctx := context.Background()

	req := newTestRequest()
	require.NoError(t, repo.Insert(ctx, req))
	_, err := repo.TransitionToHeld(ctx, req.ID, uuid.New())
	require.NoError(t, err)

	const concurrency = 10
	var wonCount int64
	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			inserted, err := cmdRepo.EnsureSubmitCommand(ctx, req.ID, req.Vendor)
			assert.NoError(t, err)
			if inserted {
				atomic.AddInt64(&wonCount, 1)
			}
		}()
	}
	wg.Wait()

	assert.Equal(t, int64(1), wonCount, "exactly one concurrent EnsureSubmitCommand call must insert")

	counts, err := cmdRepo.CountCommandsByStatuses(ctx, []string{model.CommandPending})
	require.NoError(t, err)
	assert.Equal(t, 1, counts[model.CommandPending])
}
