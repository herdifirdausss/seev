package ledger

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/herdifirdausss/seev/internal/ledger/apperror"
	"github.com/herdifirdausss/seev/internal/ledger/constant"
	"github.com/herdifirdausss/seev/internal/ledger/feepolicy"
	"github.com/herdifirdausss/seev/internal/ledger/model"
	"github.com/herdifirdausss/seev/internal/ledger/processors"
	"github.com/herdifirdausss/seev/internal/ledger/repository"
	"github.com/herdifirdausss/seev/pkg/generalerror"
	"github.com/herdifirdausss/seev/pkg/generalutil"
	"github.com/shopspring/decimal"
)

// =============================================================================
// DatabaseSQL — thin interface over the connection pool
// =============================================================================

type DatabaseSQL interface {
	WithTx(ctx context.Context, opts *sql.TxOptions, fn func(tx *sql.Tx) error) error
}

// =============================================================================
// Service
// =============================================================================

type Service struct {
	db          DatabaseSQL
	txRepo      repository.TransactionRepository
	balanceRepo repository.BalanceRepository
	entryRepo   repository.EntryRepository
	outboxRepo  repository.OutboxRepository
	registry    *processors.ProcessorRegistry
	logger      *slog.Logger
	// maxAmountPerTx is a global safety ceiling (minor units), independent
	// of any per-type/per-processor business limit — a guard against
	// bugs/abuse, not a business rule (docs/plan/10 Task T5). Zero/negative
	// disables the check entirely (treated as "no cap configured").
	maxAmountPerTx decimal.Decimal
	// feePolicy consumes a fee quote atomically inside execTransfer's own
	// tx when cmd.QuoteID is set (docs/plan/38 Task T4). nil is a valid,
	// fully-supported configuration — a QuoteID on a Command would then
	// simply be ignored (no caller in this codebase does that: transport
	// only sets QuoteID after this Service was constructed with a real
	// feePolicy — see internal/ledger.NewModule).
	feePolicy *feepolicy.Policy
}

// New constructs the posting service. AML/fraud screening no longer runs
// here (docs/plan/37): it moved to the transport layer, BEFORE this
// service's Handle is ever called, so no network round-trip happens while
// any row lock is held inside WithTx below — see
// internal/ledger/transport/http.go and pkg/fraudcheck.
func New(db DatabaseSQL, txRepo repository.TransactionRepository,
	balanceRepo repository.BalanceRepository,
	entryRepo repository.EntryRepository,
	outboxRepo repository.OutboxRepository, registry *processors.ProcessorRegistry, logger *slog.Logger,
	maxAmountPerTx decimal.Decimal, feePolicy *feepolicy.Policy) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{db: db, txRepo: txRepo, balanceRepo: balanceRepo, entryRepo: entryRepo, outboxRepo: outboxRepo, registry: registry, logger: logger, maxAmountPerTx: maxAmountPerTx, feePolicy: feePolicy}
}

// lifecycleCloseReason maps a transaction type to the closed_reason it
// writes onto its ReferenceID target when it posts successfully
// (docs/plan/14 Task T2). Types absent from this map don't close anything —
// most transaction types (money_in, transfer_p2p, adjustment_*, etc.) have
// no "original" to close. Kept here rather than on TxProcessor itself: it's
// purely a database bookkeeping concern, not business logic a processor
// author needs to think about when adding a new transaction type.
var lifecycleCloseReason = map[string]string{
	"reversal":                "reversed",
	"withdraw_settle":         "settled",
	"withdraw_cancel":         "cancelled",
	"withdraw_pending_settle": "settled",
	"withdraw_pending_cancel": "cancelled",
	"escrow_release":          "released",
	"escrow_refund":           "refunded",
}

// =============================================================================
// Handle — public entry point
// =============================================================================

func (s *Service) Handle(ctx context.Context, cmd processors.Command) (err error) {
	// Metrics + tracing (docs/plan/05 Task 1b.6). Attributes deliberately
	// exclude idempotency key and amount — those must never land in a
	// publicly-exported span.
	ctx, span := tracer.Start(ctx, "ledger.Handle", trace.WithAttributes(
		attribute.String("ledger.type", cmd.Type),
		attribute.String("ledger.idempotency_scope", cmd.IdempotencyScope),
	))
	start := time.Now()
	defer func() {
		status := "posted"
		if err != nil {
			status = "error"
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		transactionsTotal.WithLabelValues(cmd.Type, status).Inc()
		postDuration.WithLabelValues(cmd.Type).Observe(time.Since(start).Seconds())
		span.End()
	}()

	// [FIX #10 iter2] Idempotency key must not be empty
	if strings.TrimSpace(cmd.IdempotencyKey) == "" {
		return apperror.ErrEmptyIdempotencyKey
	}

	// Amounts are minor-unit integers (decision D2, docs/plan/01) — reject
	// fractional amounts before any DB work, not just at the entry-building stage.
	if !cmd.Amount.IsInteger() {
		return fmt.Errorf("%w: amount must be an integer (minor units), got %s", apperror.ErrValidation, cmd.Amount)
	}

	// Global safety ceiling (docs/plan/10 Task T5) — checked before any DB
	// work, same as the integer check above. This is NOT a business limit
	// (per-user/per-type limits belong in docs/plan/08 S1's policy layer);
	// it exists purely so a bug or abuse attempt can't post an
	// astronomically large amount. maxAmountPerTx <= 0 means unconfigured
	// (no cap).
	if s.maxAmountPerTx.IsPositive() && cmd.Amount.GreaterThan(s.maxAmountPerTx) {
		return fmt.Errorf("%w: amount %s exceeds maximum %s", apperror.ErrAmountTooLarge, cmd.Amount, s.maxAmountPerTx)
	}

	log := s.logger.With(
		slog.String("idem_key", cmd.IdempotencyKey),
		slog.String("type", cmd.Type),
		slog.String("amount", cmd.Amount.String()),
		slog.String("user_id", cmd.UserID.String()),
	)

	processor, err := s.registry.Get(cmd.Type)
	if err != nil {
		return err
	}

	// Fee quote PEEK (docs/plan/38 Task T4, discovered while implementing
	// this task — not in the original design sketch): a processor's
	// ResolveAccounts (called below, BEFORE any DB transaction opens)
	// decides whether to include the fee[gateway] system account in
	// AccountIDs based on cmd.Metadata["fee_amount"] being present
	// (resolveInlineFee in processors.go) — every processor's BuildEntries
	// later requires that account to already be resolved/locked, it cannot
	// add a new account mid-transaction. The AUTHORITATIVE, single-use,
	// atomic quote consumption still happens inside execTransfer's own tx
	// (below) — this is a non-consuming, best-effort READ purely so the
	// fee account makes it into AccountIDs before the tx opens. A quote's
	// fee_amount is immutable once created (fee_rules changes never affect
	// an already-created quote — that's the entire point of quoting), so
	// this peek can never disagree with the real consumption a few
	// milliseconds later. If the peek fails (not found/expired/wrong user),
	// metadata is simply left unset here — execTransfer's real ConsumeQuote
	// call is what returns the definitive ErrQuoteExpired/ErrQuoteMismatch.
	if cmd.QuoteID != "" && s.feePolicy != nil {
		if quoteID, perr := uuid.Parse(cmd.QuoteID); perr == nil {
			if q, gerr := s.feePolicy.GetQuote(ctx, quoteID, cmd.UserID); gerr == nil {
				if cmd.Metadata == nil {
					cmd.Metadata = map[string]any{}
				}
				cmd.Metadata["fee_amount"] = q.FeeAmount.String()
				cmd.Metadata["fee_gateway"] = q.FeeGateway
			}
		}
	}

	if err := processor.ValidateCommand(ctx, cmd); err != nil {
		return fmt.Errorf("validate command: %w", err)
	}

	resolved, currency, err := processor.ResolveAccounts(ctx, cmd)
	if err != nil {
		return fmt.Errorf("resolve accounts: %w", err)
	}

	// [docs/plan/14 Task T1] Source/Destination are explicit now (not
	// inferred from position) but MUST still point at accounts the
	// processor actually resolved — a Source/Destination outside Ordered
	// would silently write a wrong/unrelated account into
	// ledger_transactions.source_account_id, so treat it as a processor bug
	// rather than posting it.
	if resolved.Source != uuid.Nil && !accountIDIn(resolved.Ordered, resolved.Source) {
		return fmt.Errorf("processor %q: resolved Source %s is not in Ordered accounts", cmd.Type, resolved.Source)
	}
	if resolved.Destination != uuid.Nil && !accountIDIn(resolved.Ordered, resolved.Destination) {
		return fmt.Errorf("processor %q: resolved Destination %s is not in Ordered accounts", cmd.Type, resolved.Destination)
	}

	// [FIX 2026-07-11] Deduplicate only — must NOT sort. Every processor
	// indexes AccountIDs positionally (e.g. [0]=source, [1]=destination,
	// [2]=fee; see processors.go's own "Fee account IDs are always last"
	// doc). Sorting here silently swapped debit/credit direction whenever
	// a system account's UUID happened to sort before/after the user
	// account's UUID — only surfaced once run against real Postgres with
	// real UUIDs (money_out failed with INSUFFICIENT_FUNDS on the
	// settlement account because [0] was the low-numbered system account,
	// not the user's cash account as BuildEntries assumed). Lock ordering
	// for deadlock safety is handled independently by LockBalances' own
	// `ORDER BY account_id` in SQL — it does not depend on this slice's order.
	rc := processors.ResolvedCommand{
		Command:     cmd,
		AccountIDs:  generalutil.Deduplicate(resolved.Ordered),
		Currency:    currency,
		Source:      resolved.Source,
		Destination: resolved.Destination,
	}

	log.Info("transfer starting", slog.Int("accounts", len(rc.AccountIDs)), slog.String("currency", currency))

	if err := s.transfer(ctx, rc, processor); err != nil {
		// [FIX #3] ErrAlreadyPosted means idempotent success — return nil.
		// Callers must not treat a successful prior commit as an error.
		if errors.Is(err, apperror.ErrAlreadyPosted) {
			log.Info("idempotent: transaction already posted")
			return nil
		}
		log.Error("transfer failed", slog.Any("error", err))
		return err
	}

	log.Info("transfer posted")

	if err := processor.AfterCommit(ctx, cmd); err != nil {
		log.Warn("AfterCommit non-fatal error", slog.Any("error", err))
	}
	return nil
}

// =============================================================================
// transfer — retry loop with jitter
// =============================================================================

const (
	maxRetry      = 3
	baseDelayMS   = 80
	jitterRangeMS = 60
)

func (s *Service) transfer(ctx context.Context, cmd processors.ResolvedCommand, p processors.TxProcessor) error {
	var lastErr error
	for attempt := 0; attempt < maxRetry; attempt++ {
		if attempt > 0 {
			// [FIX #9] Add jitter to avoid thundering herd on concurrent retries
			delay := time.Duration(baseDelayMS+rand.Intn(jitterRangeMS)) * time.Millisecond * time.Duration(attempt)
			select {
			case <-ctx.Done():
				return fmt.Errorf("cancelled during retry backoff: %w", ctx.Err())
			case <-time.After(delay):
			}
			s.logger.Warn("retrying", slog.Int("attempt", attempt+1), slog.Any("error", lastErr))
		}
		lastErr = s.execTransfer(ctx, cmd, p)
		if lastErr == nil || !generalerror.IsRetryable(lastErr) {
			return lastErr
		}
	}
	return fmt.Errorf("failed after %d retries: %w", maxRetry, lastErr)
}

// =============================================================================
// execTransfer — one DB transaction attempt
// =============================================================================

func (s *Service) execTransfer(ctx context.Context, cmd processors.ResolvedCommand, p processors.TxProcessor) error {
	// businessErr separates "committed as failed" from "rolled back".
	// [FIX #2 iter2] Validation failures commit the header row (audit trail),
	// then return the error AFTER the commit, not inside WithTx.
	var businessErr error

	dbErr := s.db.WithTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted},
		func(tx *sql.Tx) error {
			businessErr = nil
			// Time-ordered v7, not v4 (docs/plan/11 Task T4) — keeps
			// ledger_transactions' primary-key btree insert-clustered
			// instead of scattering writes across random pages.
			txID := generalutil.NewV7()

			// ── 1. IDEMPOTENCY GATE ─────────────────────────────────────────
			// SAVEPOINT prevents PostgreSQL from aborting the whole tx on a
			// unique-violation error.
			if _, err := tx.ExecContext(ctx, `SAVEPOINT sp_idem`); err != nil {
				return fmt.Errorf("savepoint: %w", err)
			}

			// external_ref/gateway (docs/plan/16 Task T2, K5) and request_id
			// (docs/plan/36 Task T5) are purely informative correlation
			// columns — same status as source/destination_account_id above.
			// Absent for the large majority of transaction types that never
			// carry these metadata keys (transfer_p2p, adjustment_*, etc.),
			// which is why all three are nullable and read with the same
			// ok-is-optional pattern.
			externalRef, _ := generalutil.MetaString(cmd.Metadata, "external_ref")
			gateway, _ := generalutil.MetaString(cmd.Metadata, "gateway")
			requestID, _ := generalutil.MetaString(cmd.Metadata, "request_id")

			_, insertErr := tx.ExecContext(ctx, `
				INSERT INTO ledger_transactions
					(id, idempotency_key, idempotency_scope, type, status, amount, currency,
					 source_account_id, destination_account_id, external_ref, gateway, request_id,
					 created_at, updated_at)
				VALUES ($1,$2,$3,$4,'pending',$5,$6,$7,$8,$9,$10,$11,now(),now())`,
				txID,
				cmd.IdempotencyKey,
				generalutil.NullString(cmd.IdempotencyScope),
				cmd.Type,
				cmd.Amount,
				cmd.Currency,
				generalutil.NullUUID(cmd.Source),
				generalutil.NullUUID(cmd.Destination),
				generalutil.NullString(externalRef),
				generalutil.NullString(gateway),
				generalutil.NullString(requestID),
			)

			if insertErr != nil {
				_, _ = tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT sp_idem`)
				if !generalerror.IsDuplicateKey(insertErr) {
					return fmt.Errorf("insert tx header: %w", insertErr)
				}
				// [FIX #3] handleDuplicate returns ErrAlreadyPosted for "posted" —
				// Handle() converts that to nil (idempotent success).
				return s.handleDuplicate(ctx, tx, cmd.IdempotencyKey, cmd.IdempotencyScope)
			}
			_, _ = tx.ExecContext(ctx, `RELEASE SAVEPOINT sp_idem`)

			// ── 1b. FEE QUOTE CONSUMPTION (docs/plan/38 Task T4) ──────────────
			// SEGERA after the idempotency gate and BEFORE LockBalances (step
			// 2) — fail fast without ever holding a balance lock. Unlike a
			// processor business-validation failure (step 4, which commits
			// the header row as 'failed' for audit), a rejected quote
			// consumption returns the error directly: WithTx rolls back the
			// ENTIRE transaction, including the header insert above, so no
			// ledger_transactions row exists at all for a blocked attempt —
			// the same idempotency_key can be retried fresh (e.g. with a new
			// quote_id) without ever hitting handleDuplicate.
			if cmd.QuoteID != "" {
				quoteID, perr := uuid.Parse(cmd.QuoteID)
				if perr != nil {
					return apperror.NewBizErr(apperror.ErrQuoteMismatch, "quote_id is not a valid UUID")
				}
				fee, feeGateway, qerr := s.feePolicy.ConsumeQuote(ctx, tx, quoteID, cmd.UserID, cmd.Type, cmd.Currency, cmd.Amount, "tx:"+txID.String())
				if qerr != nil {
					switch {
					case errors.Is(qerr, feepolicy.ErrQuoteExpired):
						return apperror.NewBizErr(apperror.ErrQuoteExpired, qerr.Error())
					case errors.Is(qerr, feepolicy.ErrQuoteMismatch):
						return apperror.NewBizErr(apperror.ErrQuoteMismatch, qerr.Error())
					default:
						return fmt.Errorf("consume fee quote: %w", qerr)
					}
				}
				// Pushes the EXACT quoted fee into the same metadata keys
				// every processor's BuildEntries/Validate already reads
				// (fee_amount/fee_gateway, see processors.go's
				// resolveFeeAccount) — no processor code needs to know
				// quotes exist at all.
				if cmd.Metadata == nil {
					cmd.Metadata = map[string]any{}
				}
				cmd.Metadata["fee_amount"] = fee.String()
				cmd.Metadata["fee_gateway"] = feeGateway
			}

			// ── 2. SPLIT ACCOUNTS & LOCK ONLY USER ONES ───────────────────────
			// [docs/plan/11 Task T1] account_balances.allow_negative is true
			// ONLY for system accounts (settlement/adjustment/chargeback —
			// see migrations/000001). Those never need a FOR UPDATE lock:
			// they have no overdraft floor to protect, so their balance is
			// updated later via an atomic `balance = balance + delta`
			// (ApplySystemDeltas) instead of a pre-read-then-write. This is
			// what eliminates the old hot-row bottleneck where every
			// money_in/money_out through the same gateway serialized on
			// that gateway's settlement account row lock for the ENTIRE
			// validate→build→insert pipeline, not just the final write.
			//
			// flags is an UNLOCKED read of every cmd.AccountIDs row —used
			// only to decide the split and, for system accounts, to satisfy
			// structural validation (status/currency). Safe unlocked:
			// allow_negative is immutable post-provisioning, and no
			// processor or validator ever reads a system account's Balance
			// field for arithmetic (only SufficientFundsValidator inspects
			// Balance, and it only ever targets AccountIDs[0], which for
			// every registered processor is a user account — see
			// docs/plan/11-phase2b-efficiency-locking.md Task T1 for the
			// audit that established this).
			flags, err := s.balanceRepo.GetAccountFlags(ctx, tx, cmd.AccountIDs)
			if err != nil {
				return err
			}
			var userIDs, systemIDs []uuid.UUID
			systemIDSet := make(map[uuid.UUID]bool, len(cmd.AccountIDs))
			for _, id := range cmd.AccountIDs {
				ab, ok := flags[id]
				if !ok {
					continue // missing row — validateAccounts below reports this
				}
				if ab.AllowNegative {
					systemIDs = append(systemIDs, id)
					systemIDSet[id] = true
				} else {
					userIDs = append(userIDs, id)
				}
			}

			// ORDER BY account_id (UUID bytes) inside LockBalances is
			// deterministic across all concurrent transactions — prevents
			// deadlock. Only user accounts are ever passed here now.
			userBalances, err := s.balanceRepo.LockBalances(ctx, tx, userIDs)
			if err != nil {
				return err
			}

			// merged = userBalances (locked, accurate) + system accounts'
			// unlocked snapshot from flags — passed to validateAccounts and
			// to the processor interface unchanged, so no processor code
			// needs to know about this split at all.
			merged := make(map[uuid.UUID]model.AccountBalance, len(cmd.AccountIDs))
			for id, ab := range userBalances {
				merged[id] = ab
			}
			for _, id := range systemIDs {
				merged[id] = flags[id]
			}

			// ── 3. STRUCTURAL VALIDATION ─────────────────────────────────────
			if err := s.validateAccounts(cmd, merged); err != nil {
				return err // structural error → rollback (not committed as 'failed')
			}

			// ── 4. BUSINESS VALIDATION (processor) ──────────────────────────
			if err := p.Validate(ctx, tx, cmd, merged); err != nil {
				if markErr := s.markFailed(ctx, tx, txID, err.Error()); markErr != nil {
					return fmt.Errorf("mark failed after validation: %w", markErr)
				}
				businessErr = err
				return nil // → WithTx commits with status='failed'
			}

			// ── 4b. CLOSE ORIGINAL (lifecycle guard) ─────────────────────────
			// [docs/plan/14 Task T2, decision K3] Reversal/settle/cancel/
			// release/refund each "close" a prior transaction. This single
			// conditional UPDATE (WHERE closed_by_tx_id IS NULL) is the
			// race-proof guard against two concurrent closers of the same
			// original (double-reversal, settle-after-cancel) — it does not
			// depend on the per-processor Validate() checks above, which run
			// against an unlocked read and are only a fast-fail convenience.
			// Losing the race is a business outcome (commit as 'failed'),
			// not an infra error.
			if reason, closes := lifecycleCloseReason[cmd.Type]; closes {
				rows, err := s.txRepo.CloseOriginal(ctx, tx, cmd.ReferenceID, txID, reason)
				if err != nil {
					return fmt.Errorf("close original: %w", err)
				}
				if rows == 0 {
					bizErr := apperror.NewBizErr(apperror.ErrAlreadyClosed,
						fmt.Sprintf("transaction %s was already closed by another request", cmd.ReferenceID))
					if markErr := s.markFailed(ctx, tx, txID, bizErr.Error()); markErr != nil {
						return fmt.Errorf("mark failed after close guard: %w", markErr)
					}
					businessErr = bizErr
					return nil // → WithTx commits with status='failed'
				}
			}

			// ── 5. BUILD ENTRIES ─────────────────────────────────────────────
			// [FIX #1 iter3] BuildEntries receives tx — Reversal can re-query
			// entries inside the transaction (no TOCTOU, no value-copy bug).
			entries, err := p.BuildEntries(ctx, tx, cmd, merged)
			if err != nil {
				return fmt.Errorf("build entries: %w", err)
			}

			// ── 6. VALIDATE ENTRIES BALANCED ─────────────────────────────────
			if err := validateBalanced(entries); err != nil {
				return err // programming error in processor
			}

			// ── 7. COMPUTE NEW BALANCES ────────────────────────────────────────
			// User accounts: single-pass computation from their LOCKED,
			// accurate balances — [FIX #4] one computation feeds both
			// InsertEntries and UpdateBalances so they can never diverge.
			// System accounts: NOT computed here — computeSystemDeltas only
			// sums each account's net entry delta; the actual new balance
			// comes from ApplySystemDeltas' atomic RETURNING in step 7b,
			// never from arithmetic on the stale unlocked snapshot in
			// `merged` (that would silently lose concurrent updates from
			// another in-flight transaction touching the same system
			// account — the exact race this redesign exists to remove).
			userEntries, systemEntries := splitEntriesByAccount(entries, systemIDSet)
			userNewBalances := applyEntries(userBalances, userEntries)

			// ── 7b. APPLY SYSTEM DELTAS (atomic, no lock needed) ──────────────
			systemDeltas := computeSystemDeltas(systemEntries)
			systemNewBalances, err := s.balanceRepo.ApplySystemDeltas(ctx, tx, systemDeltas)
			if err != nil {
				return err
			}

			allNewBalances := make(map[uuid.UUID]decimal.Decimal, len(userNewBalances)+len(systemNewBalances))
			for id, bal := range userNewBalances {
				allNewBalances[id] = bal
			}
			for id, bal := range systemNewBalances {
				allNewBalances[id] = bal
			}

			// ── 8. INSERT LEDGER ENTRIES ─────────────────────────────────────
			// entries still contains BOTH user and system account entries —
			// allNewBalances now has a balance_after for every one of them.
			if err := s.entryRepo.InsertEntries(ctx, tx, txID, entries, allNewBalances); err != nil {
				return err
			}

			// ── 9. UPDATE BALANCE PROJECTIONS (user accounts only) ────────────
			// System accounts were already persisted atomically in step 7b —
			// updating them again here would be redundant (and, using the
			// stale `merged` snapshot, actively wrong).
			if err := s.balanceRepo.UpdateBalances(ctx, tx, userNewBalances); err != nil {
				return err
			}

			// ── 10. MARK POSTED ───────────────────────────────────────────────
			if _, err := tx.ExecContext(ctx,
				`UPDATE ledger_transactions SET status='posted', updated_at=now() WHERE id=$1`, txID,
			); err != nil {
				return fmt.Errorf("mark posted: %w", err)
			}

			// ── 11. OUTBOX (same transaction) ─────────────────────────────────
			if err := s.outboxRepo.InsertEvents(ctx, tx, p.OutboxEvents(cmd, txID, entries)); err != nil {
				return err
			}

			return nil
		})

	if dbErr != nil {
		return dbErr
	}
	return businessErr
}

func accountIDIn(ids []uuid.UUID, id uuid.UUID) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

// =============================================================================
// validateAccounts — structural checks (existence, status, currency)
// =============================================================================

func (s *Service) validateAccounts(cmd processors.ResolvedCommand, balances map[uuid.UUID]model.AccountBalance) error {
	for _, id := range cmd.AccountIDs {
		ab, ok := balances[id]
		if !ok {
			return fmt.Errorf("%w: %s (missing account_balances row)", apperror.ErrAccountNotFound, id)
		}
		switch ab.Status {
		case constant.AccountStatusSuspended:
			return fmt.Errorf("%w: %s", apperror.ErrAccountSuspended, id)
		case constant.AccountStatusClosed:
			return fmt.Errorf("%w: %s", apperror.ErrAccountClosed, id)
		case constant.AccountStatusActive:
			// ok
		default:
			return fmt.Errorf("%w: unknown status %q on account %s", apperror.ErrValidation, ab.Status, id)
		}
		if ab.Currency != cmd.Currency {
			return fmt.Errorf("%w: account %s has %s, tx expects %s",
				apperror.ErrCurrencyMismatch, id, ab.Currency, cmd.Currency)
		}
	}
	return nil
}

// =============================================================================
// applyEntries — single-pass balance computation  [FIX #4]
// =============================================================================

// applyEntries computes the final balance for every affected account.
// Result is used for BOTH ledger_entries.balance_after AND account_balances.balance,
// guaranteeing they are always derived from the same calculation.
func applyEntries(balances map[uuid.UUID]model.AccountBalance, entries []model.EntryInstruction) map[uuid.UUID]decimal.Decimal {
	result := make(map[uuid.UUID]decimal.Decimal, len(balances))
	for id, ab := range balances {
		result[id] = ab.Balance
	}
	for _, e := range entries {
		switch e.Direction {
		case constant.Debit:
			result[e.AccountID] = result[e.AccountID].Sub(e.Amount)
		case constant.Credit:
			result[e.AccountID] = result[e.AccountID].Add(e.Amount)
		}
	}
	return result
}

// =============================================================================
// splitEntriesByAccount / computeSystemDeltas (docs/plan/11 Task T1)
// =============================================================================

// splitEntriesByAccount partitions entries into the ones touching a system
// (unlocked) account and the ones touching a user (locked) account, so each
// half can be fed to the appropriate balance-update path.
func splitEntriesByAccount(entries []model.EntryInstruction, systemIDs map[uuid.UUID]bool) (userEntries, systemEntries []model.EntryInstruction) {
	for _, e := range entries {
		if systemIDs[e.AccountID] {
			systemEntries = append(systemEntries, e)
		} else {
			userEntries = append(userEntries, e)
		}
	}
	return userEntries, systemEntries
}

// computeSystemDeltas sums each system account's net entry delta (credit
// positive, debit negative) — the value ApplySystemDeltas adds atomically
// to whatever the row currently holds. Unlike applyEntries, this never
// starts from a pre-read balance: a system account's actual new balance is
// only ever known authoritatively from ApplySystemDeltas' own `RETURNING`.
func computeSystemDeltas(systemEntries []model.EntryInstruction) map[uuid.UUID]decimal.Decimal {
	deltas := make(map[uuid.UUID]decimal.Decimal, len(systemEntries))
	for _, e := range systemEntries {
		switch e.Direction {
		case constant.Debit:
			deltas[e.AccountID] = deltas[e.AccountID].Sub(e.Amount)
		case constant.Credit:
			deltas[e.AccountID] = deltas[e.AccountID].Add(e.Amount)
		}
	}
	return deltas
}

// =============================================================================
// markFailed
// =============================================================================

func (s *Service) markFailed(ctx context.Context, tx *sql.Tx, txID uuid.UUID, msg string) error {
	if len(msg) > 500 {
		msg = msg[:500]
	}
	_, err := tx.ExecContext(ctx, `
		UPDATE ledger_transactions SET status='failed', error_message=$1, updated_at=now() WHERE id=$2`,
		msg, txID)
	return err
}

// =============================================================================
// handleDuplicate
// =============================================================================

func (s *Service) handleDuplicate(ctx context.Context, tx *sql.Tx, key, scope string) error {
	var status string
	err := tx.QueryRowContext(ctx, `
		SELECT status FROM ledger_transactions
		WHERE  idempotency_key=$1
		  AND (idempotency_scope=$2 OR ($2 IS NULL AND idempotency_scope IS NULL))
		LIMIT 1`, key, generalutil.NullString(scope)).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("idempotency record vanished after duplicate error (race)")
	}
	if err != nil {
		return fmt.Errorf("lookup duplicate: %w", err)
	}
	switch status {
	case "posted":
		return apperror.ErrAlreadyPosted // Handle() converts this to nil [FIX #3]
	case "failed":
		return apperror.ErrPreviousFailed
	default:
		return apperror.ErrStillProcessing
	}
}

// =============================================================================
// validateBalanced
// =============================================================================

func validateBalanced(entries []model.EntryInstruction) error {
	var totalD, totalC decimal.Decimal
	for _, e := range entries {
		if !e.Amount.IsPositive() {
			return fmt.Errorf("%w: entry amount must be positive, got %s", apperror.ErrValidation, e.Amount)
		}
		switch e.Direction {
		case constant.Debit:
			totalD = totalD.Add(e.Amount)
		case constant.Credit:
			totalC = totalC.Add(e.Amount)
		}
	}
	if !totalD.Equal(totalC) {
		return fmt.Errorf("%w: constant.Debit=%s constant.Credit=%s", apperror.ErrUnbalancedEntries, totalD, totalC)
	}
	return nil
}
