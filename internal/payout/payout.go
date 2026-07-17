// Package payout is the public facade for the payout module (docs/plan/23,
// decision K-T3/K-T6) — orchestrates a user withdraw request through
// hold -> vendor submission -> terminal state (settled/cancelled/failed).
// This is the ONLY package other code may import from internal/payout —
// importing internal/payout/repository or internal/payout/model directly
// from outside this module is a boundary violation
// (docs/plan/01-target-architecture.md, enforced by boundary_test.go).
//
// payout is NOT a general-purpose saga framework — it is a state machine
// (payout_requests.status) plus a resume/polling job that re-drives
// whatever step a crashed/interrupted request last reached. The guard that
// actually prevents money-unsafe outcomes (double-settle,
// settle-after-cancel) is the LEDGER's own closed_by_tx_id atomic guard
// (docs/plan/14 Task T2, decision K3) — payout translates a lost race
// (ledgererr.ErrAlreadyClosed) into "reconcile local state", it never builds
// its own competing protection.
package payout

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/shopspring/decimal"
	"google.golang.org/grpc"

	payoutv1 "github.com/herdifirdausss/seev/gen/payout/v1"
	"github.com/herdifirdausss/seev/internal/payout/grpcserver"
	"github.com/herdifirdausss/seev/internal/payout/model"
	"github.com/herdifirdausss/seev/internal/payout/repository"
	"github.com/herdifirdausss/seev/internal/payout/worker"
	"github.com/herdifirdausss/seev/internal/vendorgw"
	"github.com/herdifirdausss/seev/pkg/database"
	"github.com/herdifirdausss/seev/pkg/fraudcheck"
	"github.com/herdifirdausss/seev/pkg/ledgerclient"
	"github.com/herdifirdausss/seev/pkg/scheduler"
)

// Re-exported types so callers never need to import internal/payout/model.
type PayoutRequest = model.PayoutRequest

func (m *Module) RegisterGRPC(server *grpc.Server) {
	payoutv1.RegisterPayoutServiceServer(server, grpcserver.New(m, repository.ErrNotFound, ErrNoRoute, ErrNoVendorAvailable, ErrScreeningBlocked))
}

// Poster is the subset of ledger.Module's behavior payout needs — a local
// structural interface (mirrors internal/payin.Poster, docs/plan/22 Task
// T2) rather than a dependency on the concrete *ledger.Module type.
type Poster interface {
	Post(ctx context.Context, cmd ledgerclient.Command) error
	// GetTransactionByIdempotencyKey recovers the tx ID Post() itself
	// doesn't return, so payout can later pass it as ReferenceID to
	// withdraw_settle/withdraw_cancel (docs/plan/23 Task T3).
	GetTransactionByIdempotencyKey(ctx context.Context, key, scope string) (ledgerclient.Transaction, error)
	// GetUserCurrency resolves the currency Create should record on a new
	// payout_requests row (docs/plan/23 Task T3 step 1).
	GetUserCurrency(ctx context.Context, userID uuid.UUID, pocketCode string) (string, error)
	// ResolveFee prices the withdraw fee (docs/plan/25 Task T2) — the
	// boundary-clean way payout charges a fee without importing
	// internal/ledger/feepolicy (a subpackage of another module). settle()
	// calls this and passes the result as fee_amount/fee_gateway metadata
	// on the withdraw_settle command — NEVER on withdraw_initiate/cancel,
	// since a fee charged upfront would either break the exact-amount
	// close validation or strand the fee on a cancelled withdrawal.
	ResolveFee(ctx context.Context, userID uuid.UUID, txType, gateway, currency string, amount decimal.Decimal) (fee decimal.Decimal, feeGateway string, ok bool, err error)
	// ConsumeFeeQuote atomically, single-use consumes a fee quote created
	// via ledger's POST /fees/quote (docs/plan/38 Task T5) — Create calls
	// this BEFORE hold (anti-burn ordering: quote consumption never moves
	// money by itself). Returns *ledgererr.LedgerError{Code: "QUOTE_EXPIRED"
	// | "QUOTE_MISMATCH"} on rejection.
	ConsumeFeeQuote(ctx context.Context, quoteID, userID uuid.UUID, txType, currency string, amount decimal.Decimal, ref string) (fee decimal.Decimal, feeGateway string, err error)
}

// Module is the public facade for the payout module.
type Module struct {
	repo     repository.Repository
	routing  repository.RoutingRepository
	poster   Poster
	registry *vendorgw.Registry
	logger   *slog.Logger
	// fraudClient screens a payout before any row is created or hold is
	// posted (docs/plan/37 Task T5). nil is a valid, fully-supported
	// configuration — no screening runs.
	fraudClient *fraudcheck.Client
	// breaker tracks per-vendor circuit health (docs/plan/40 Task T1) — nil
	// is a valid, fully-supported configuration (byte-identical to before
	// this feature existed: every registered vendor is always "allowed").
	breaker *vendorgw.HealthTracker

	resumeJob *worker.ResumeJob
}

// NewModule wires the payout module. Vendor and gateway selection comes
// from the routing repository. redisClient follows the optional-Redis convention as
// ledger.NewModule: nil means the resume job's distributed lock falls back
// to an in-memory implementation (single-instance only). fraudClient may be
// nil to disable pre-hold fraud screening entirely. breaker may be nil to
// disable circuit-breaking entirely (every registered vendor is always
// allowed).
func NewModule(db database.DatabaseSQL, poster Poster, registry *vendorgw.Registry, redisClient *redis.Client, logger *slog.Logger, fraudClient *fraudcheck.Client, breaker *vendorgw.HealthTracker) *Module {
	if logger == nil {
		logger = slog.Default()
	}
	m := &Module{
		repo:        repository.NewRepository(db),
		routing:     repository.NewRoutingRepository(db),
		poster:      poster,
		registry:    registry,
		logger:      logger,
		fraudClient: fraudClient,
		breaker:     breaker,
	}

	var lock scheduler.LockProvider
	if redisClient != nil {
		instanceID, err := os.Hostname()
		if err != nil || instanceID == "" {
			instanceID = uuid.NewString()
		}
		lock = scheduler.NewRedisLock(redisClient, instanceID)
	} else {
		lock = scheduler.NewMemoryLock(time.Second)
	}
	m.resumeJob = worker.NewResumeJob(m, lock, logger, time.Minute)

	return m
}

// StartWorkers launches the resume/polling job (docs/plan/23 Task T3 step
// 3). Call StopWorkers on shutdown.
func (m *Module) StartWorkers(ctx context.Context) {
	if err := m.resumeJob.Start(ctx); err != nil {
		m.logger.Error("payout: failed to start resume job", slog.Any("error", err))
	}
}

// StopWorkers gracefully stops the resume job, waiting for any in-flight
// run. Safe to call even if StartWorkers was never called.
func (m *Module) StopWorkers() {
	m.resumeJob.Stop()
}
