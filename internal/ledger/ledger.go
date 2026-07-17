// Package ledger is the public facade for the ledger module — a
// double-entry, append-only accounting engine with an idempotent,
// concurrency-safe posting pipeline and a transactional outbox for reliable
// event publishing.
//
// This is the ONLY package other modules or cmd/gateway may import from
// internal/ledger — importing any subpackage (repository, processors,
// service/*, transport) directly from outside the module is a boundary
// violation (docs/plan/01-target-architecture.md).
package ledger

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/shopspring/decimal"
	"google.golang.org/grpc"

	ledgerv1 "github.com/herdifirdausss/seev/gen/ledger/v1"
	"github.com/herdifirdausss/seev/internal/ledger/apperror"
	"github.com/herdifirdausss/seev/internal/ledger/constant"
	"github.com/herdifirdausss/seev/internal/ledger/feepolicy"
	"github.com/herdifirdausss/seev/internal/ledger/grpcserver"
	"github.com/herdifirdausss/seev/internal/ledger/model"
	"github.com/herdifirdausss/seev/internal/ledger/processors"
	"github.com/herdifirdausss/seev/internal/ledger/repository"
	"github.com/herdifirdausss/seev/internal/ledger/service/accrual"
	"github.com/herdifirdausss/seev/internal/ledger/service/adjustments"
	"github.com/herdifirdausss/seev/internal/ledger/service/disbursement"
	ledgerhandle "github.com/herdifirdausss/seev/internal/ledger/service/handle"
	"github.com/herdifirdausss/seev/internal/ledger/service/provision"
	"github.com/herdifirdausss/seev/internal/ledger/service/recon"
	"github.com/herdifirdausss/seev/internal/ledger/service/schedule"
	"github.com/herdifirdausss/seev/internal/ledger/transport"
	"github.com/herdifirdausss/seev/internal/ledger/worker"
	"github.com/herdifirdausss/seev/pkg/alerting"
	"github.com/herdifirdausss/seev/pkg/currency"
	"github.com/herdifirdausss/seev/pkg/database"
	"github.com/herdifirdausss/seev/pkg/fraudcheck"
	"github.com/herdifirdausss/seev/pkg/messaging"
	"github.com/herdifirdausss/seev/pkg/scheduler"
)

// Re-exported types so callers never need to import internal/ledger
// subpackages to use this facade.
type (
	Command     = processors.Command
	Account     = model.Account
	Balance     = model.AccountBalance
	Transaction = model.LedgerTransaction
	Entry       = model.LedgerEntry
	Statement   = model.Statement
	// PendingAdjustment is a maker-checker adjustment request (docs/plan/16
	// Task T1).
	PendingAdjustment = model.PendingAdjustment
	// ReconBatchReport is an imported settlement batch's report (docs/plan/16
	// Task T2).
	ReconBatchReport = model.ReconBatchReport
	ReconImportRow   = model.ReconImportRow
	// ReconBatch is one imported settlement batch header, listing-only
	// (docs/plan/25 Task T5).
	ReconBatch = model.ReconBatch
	// DeadOutboxEvent is one dead-lettered outbox event, listing-only
	// (docs/plan/25 Task T5).
	DeadOutboxEvent = model.DeadOutboxEvent
	// ScheduledTransaction is a recurring/deferred user transaction
	// (docs/plan/19 Task T1).
	ScheduledTransaction = model.ScheduledTransaction
	// DisbursementImportRow/DisbursementBatchReport/DisbursementRunResult
	// back batch disbursement (docs/plan/19 Task T2).
	DisbursementImportRow   = model.DisbursementImportRow
	DisbursementBatchReport = model.DisbursementBatchReport
	DisbursementRunResult   = model.DisbursementRunResult
	// SavingsConfig marks an account as interest-bearing (docs/plan/19 Task T3).
	SavingsConfig = model.SavingsConfig
	// ReportDailyPosition/ReportDailyMutation/ReportReconSummary back the
	// regulatory reporting endpoints (docs/plan/20 Task T2).
	ReportDailyPosition = model.ReportDailyPosition
	ReportDailyMutation = model.ReportDailyMutation
	ReportReconSummary  = model.ReportReconSummary
	// PolicyChecker is satisfied structurally by internal/policy.Engine
	// (docs/plan/17 Task T1) — re-exported so callers can name the type
	// without importing internal/ledger/transport; they never need to
	// import internal/policy either, since Go interface satisfaction is
	// structural (pass the concrete *policy.Engine value straight to
	// NewModule).
	PolicyChecker = transport.PolicyChecker
	// LedgerError is the structured error Post/Handle return for business
	// validation failures (never for infra errors) — re-exported so a
	// caller outside this module (e.g. internal/payin, docs/plan/22 Task
	// T2) can classify "business failure, won't heal on retry" vs "infra
	// error, retry/redeliver" via errors.As(err, &ledgerErr), the same
	// pattern internal/ledger/service/schedule already uses internally.
	LedgerError = apperror.LedgerError
	// Quote is a fee quote row (docs/plan/38) — re-exported so a caller
	// outside this module (e.g. internal/testutil's LedgerHarness, used by
	// internal/payout's own integration tests to create a quote to consume)
	// can name CreateQuote's return type without importing the
	// module-private internal/ledger/feepolicy package directly.
	Quote = feepolicy.Quote
)

// ErrAlreadyClosed is returned by Post when a lifecycle-closing command
// (withdraw_settle, withdraw_cancel, reversal, ...) loses the atomic
// closed_by_tx_id race (docs/plan/14 Task T2, decision K3) — re-exported so
// a caller outside this module (e.g. internal/payout, docs/plan/23 Task T4)
// can distinguish "someone else already closed this" (re-read state,
// reconcile, not a real error) from every other business/infra failure via
// errors.Is(err, ledger.ErrAlreadyClosed). Payout deliberately does NOT
// build its own double-settle protection — this sentinel is how it detects
// that the ledger's own guard (the sole source of truth) already fired.
var ErrAlreadyClosed = apperror.ErrAlreadyClosed

// ErrQuoteExpired/ErrQuoteMismatch (docs/plan/38 Task T5) are re-exported so
// a caller outside this module (payout, via ConsumeFeeQuote below) can
// classify a rejected quote consumption the same way any other business
// error is classified — errors.Is(err, ledger.ErrQuoteExpired) — without
// needing to import the module-private internal/ledger/feepolicy package.
var ErrQuoteExpired = apperror.ErrQuoteExpired
var ErrQuoteMismatch = apperror.ErrQuoteMismatch

// WorkerConfig tunes the ledger module's background workers (outbox relay +
// integrity verifier). Deliberately independent of internal/config — the
// module must not depend on the composition root's config type.
type WorkerConfig struct {
	Enabled            bool
	OutboxPollInterval time.Duration
	OutboxBatchSize    int
	// AlertWebhookURL, if non-empty, is POSTed to on every integrity
	// discrepancy the verifier finds (docs/plan/12 Task T4). Empty = no
	// external alert, log+metric only (backward compatible default).
	AlertWebhookURL string
}

// Module is the public facade for the ledger module.
type Module struct {
	handleSvc       *ledgerhandle.Service
	provisionSvc    *provision.Service
	adjustmentsSvc  *adjustments.Service
	reconSvc        *recon.Service
	scheduleSvc     *schedule.Service
	disbursementSvc *disbursement.Service
	accrualSvc      *accrual.Service

	accountRepo      repository.AccountRepository
	balanceRepo      repository.BalanceRepository
	txRepo           repository.TransactionRepository
	entryRepo        repository.EntryRepository
	outboxRepo       repository.OutboxRepository
	snapshotRepo     repository.SnapshotRepository
	currencyRepo     repository.CurrencyRepository
	scheduleRepo     repository.ScheduledTransactionRepository
	disbursementRepo repository.DisbursementRepository
	savingsRepo      repository.SavingsRepository
	reportingRepo    repository.ReportingRepository
	kycTierRepo      repository.KycTierRepository

	router            http.Handler
	policyChecker     PolicyChecker
	feePolicy         *feepolicy.Policy
	processorRegistry *processors.ProcessorRegistry

	broker      messaging.Broker
	workerCfg   WorkerConfig
	outboxRelay *worker.OutboxRelay
	verifier    *worker.Verifier
	snapshotJob *worker.SnapshotJob
	scheduleJob *worker.ScheduleRunnerJob
	accrualJob  *worker.AccrualJob
	// loc is Asia/Jakarta (or UTC as a load-failure fallback) — the single
	// timezone every calendar-day boundary in this module (snapshots,
	// statements) is computed against.
	loc    *time.Location
	logger *slog.Logger
}

// NewModule wires the ledger module's internals: repositories, the posting
// engine, the provisioning service, the HTTP transport layer, and the
// background workers (outbox relay + integrity verifier).
//
// redisClient backs the verifier's distributed lock so only one process
// replica runs each scheduled check; pass nil to fall back to an in-memory
// lock (fine for a single-instance deployment, NOT safe for multi-replica).
//
// maxAmountPerTx is a global safety ceiling (minor units) applied to every
// posted transaction — zero/negative disables it (docs/plan/10 Task T5).
//
// policyChecker, if non-nil, is evaluated before every posting on the
// PUBLIC router only (docs/plan/17 Task T1) — the internal router never
// applies it (trusted internal callers aren't subject to end-user velocity
// limits). Pass nil to disable policy checks entirely — byte-identical
// behavior to before this parameter existed.
//
// fraudClient, if non-nil, screens every PUBLIC-router posting BEFORE any
// DB transaction opens (docs/plan/37) — nil disables screening entirely,
// same convention as policyChecker. This replaced the old in-transaction
// PrePostHook seam (docs/plan/20): screening moved out of
// internal/ledger/service/handle entirely, into the transport layer, so no
// network round-trip ever happens while a row lock is held.
func NewModule(db database.DatabaseSQL, broker messaging.Broker, redisClient *redis.Client, workerCfg WorkerConfig, logger *slog.Logger, maxAmountPerTx decimal.Decimal, policyChecker PolicyChecker, fraudClient *fraudcheck.Client, feeQuoteTTL time.Duration) *Module {
	if logger == nil {
		logger = slog.Default()
	}

	accountRepo := repository.NewAccountRepository(db)
	txRepo := repository.NewTransactionRepository(db)
	balanceRepo := repository.NewBalanceRepository(db)
	entryRepo := repository.NewEntryRepository(db)
	outboxRepo := repository.NewOutboxRepository(db)
	registry := processors.NewDefaultRegistry(accountRepo, txRepo)

	loc, err := time.LoadLocation("Asia/Jakarta")
	if err != nil {
		loc = time.UTC
	}
	snapshotRepo := repository.NewSnapshotRepository(db, loc)
	adjRepo := repository.NewPendingAdjustmentRepository(db)
	reconRepo := repository.NewReconRepository(db)
	currencyRepo := repository.NewCurrencyRepository(db)
	scheduleRepo := repository.NewScheduledTransactionRepository(db)
	disbursementRepo := repository.NewDisbursementRepository(db)
	savingsRepo := repository.NewSavingsRepository(db)
	reportingRepo := repository.NewReportingRepository(db)
	kycTierRepo := repository.NewKycTierRepository(db)

	feeQuotePolicy := feepolicy.New(db, repository.NewFeeRepository(db))
	handleSvc := ledgerhandle.New(db, txRepo, balanceRepo, entryRepo, outboxRepo, registry, logger, maxAmountPerTx, feeQuotePolicy)
	adjustmentsSvc := adjustments.New(db, adjRepo, txRepo, outboxRepo, handleSvc)
	scheduleSvc := schedule.New(db, scheduleRepo, handleSvc, logger)
	disbursementSvc := disbursement.New(db, disbursementRepo, txRepo, handleSvc)
	accrualSvc := accrual.New(db, savingsRepo, snapshotRepo, handleSvc, logger)

	m := &Module{
		handleSvc:         handleSvc,
		provisionSvc:      provision.New(db, repository.NewProvisioningRepository()),
		adjustmentsSvc:    adjustmentsSvc,
		reconSvc:          recon.New(db, reconRepo, adjustmentsSvc),
		scheduleSvc:       scheduleSvc,
		disbursementSvc:   disbursementSvc,
		accrualSvc:        accrualSvc,
		accountRepo:       accountRepo,
		balanceRepo:       balanceRepo,
		txRepo:            txRepo,
		entryRepo:         entryRepo,
		outboxRepo:        outboxRepo,
		snapshotRepo:      snapshotRepo,
		currencyRepo:      currencyRepo,
		scheduleRepo:      scheduleRepo,
		disbursementRepo:  disbursementRepo,
		savingsRepo:       savingsRepo,
		reportingRepo:     reportingRepo,
		kycTierRepo:       kycTierRepo,
		broker:            broker,
		workerCfg:         workerCfg,
		loc:               loc,
		logger:            logger,
		processorRegistry: registry,
	}
	m.policyChecker = policyChecker
	m.feePolicy = feeQuotePolicy
	m.router = transport.NewRouterWithFraud(m, policyChecker, m.feePolicy, fraudClient, logger, feeQuoteTTL)

	m.outboxRelay = worker.NewOutboxRelay(outboxRepo, broker, logger, worker.OutboxRelayConfig{
		PollInterval: workerCfg.OutboxPollInterval,
		BatchSize:    workerCfg.OutboxBatchSize,
	})

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
	var alertFn alerting.AlertFunc
	if workerCfg.AlertWebhookURL != "" {
		alertFn = alerting.NewWebhookAlerter(workerCfg.AlertWebhookURL, nil)
	}
	m.verifier = worker.NewVerifier(repository.NewVerificationRepository(db), outboxRepo, lock, logger, loc, alertFn)
	m.snapshotJob = worker.NewSnapshotJob(snapshotRepo, lock, logger, loc, alertFn)
	m.scheduleJob = worker.NewScheduleRunnerJob(scheduleSvc, lock, logger, loc)
	m.accrualJob = worker.NewAccrualJob(accrualSvc, lock, logger, loc)

	return m
}

// IsKnownTransactionType validates admin-managed configuration against the
// same processor registry used by the posting engine.
func (m *Module) IsKnownTransactionType(txType string) bool {
	_, err := m.processorRegistry.Get(txType)
	return err == nil
}

// Router returns the public-facing HTTP handler for the ledger module — only
// transaction types safe for direct end-user use are postable through it
// (docs/plan/10 Task T1). The caller mounts it under a path prefix and wraps
// it with auth/rate-limit middleware.
func (m *Module) Router() http.Handler {
	return m.router
}

// ResolveFee prices a transaction for one user. ok=false means no enabled
// database rule matched (or the resolved rule would produce an invalid fee).
func (m *Module) ResolveFee(ctx context.Context, userID uuid.UUID, txType, gateway, currency string, amount decimal.Decimal) (fee decimal.Decimal, feeGateway string, ok bool) {
	return m.feePolicy.Resolve(ctx, userID, txType, gateway, currency, amount)
}

// CreateQuote prices and persists a single-use fee quote (docs/plan/38 Task
// T2/T3) — the same path POST /fees/quote calls. Exposed on the module
// (rather than only reachable via HTTP) so internal/testutil's
// LedgerHarness can create quotes for integration tests (e.g.
// internal/payout's own) without any caller needing to import the
// module-private internal/ledger/feepolicy package — see the Quote
// re-export above.
func (m *Module) CreateQuote(ctx context.Context, userID uuid.UUID, txType, gateway, currency string, amount decimal.Decimal, ttl time.Duration) (Quote, error) {
	return m.feePolicy.CreateQuote(ctx, userID, txType, gateway, currency, amount, ttl)
}

// ConsumeFeeQuote atomically, single-use consumes a fee quote created via
// POST /fees/quote (docs/plan/38 Task T5) — a short, standalone operation
// (no ledger posting tx involved), exposed over gRPC so payout-service (a
// separate process with no direct access to seev_ledger) can spend a quote
// before it holds funds. Rejection surfaces as *apperror.LedgerError (via
// apperror.ErrQuoteExpired/ErrQuoteMismatch — the same re-exported sentinels
// execTransfer's own quote consumption uses, docs/plan/38 Task T4) rather
// than the raw feepolicy sentinels, so every caller outside this module —
// grpcserver's gRPC mapping AND internal/testutil's in-process harness — can
// classify it through the ONE existing generic apperror.LedgerError path
// (mapError / translateLedgerErr) instead of needing its own feepolicy
// import, which internal/ledger/feepolicy being module-private forbids.
func (m *Module) ConsumeFeeQuote(ctx context.Context, quoteID, userID uuid.UUID, txType, currency string, amount decimal.Decimal, ref string) (fee decimal.Decimal, feeGateway string, err error) {
	fee, feeGateway, err = m.feePolicy.ConsumeQuoteStandalone(ctx, quoteID, userID, txType, currency, amount, ref)
	switch {
	case err == nil:
		return fee, feeGateway, nil
	case errors.Is(err, feepolicy.ErrQuoteExpired):
		return decimal.Zero, "", apperror.NewBizErr(apperror.ErrQuoteExpired, err.Error())
	case errors.Is(err, feepolicy.ErrQuoteMismatch):
		return decimal.Zero, "", apperror.NewBizErr(apperror.ErrQuoteMismatch, err.Error())
	default:
		return decimal.Zero, "", err
	}
}

// ApplyKycTier upserts userID's effective policy_limits from the
// policy_tier_limits template for kycLevel (docs/plan/39 Task T5) — called
// by auth-service's gRPC ApplyKycTier when a KYC submission is approved.
// Idempotent (re-applying the same level is a no-op; upgrading/downgrading
// overwrites in place). Returns apperror.ErrUnknownKycTier if kycLevel
// matches zero template rows — a caller input error, not a business-state
// failure.
func (m *Module) ApplyKycTier(ctx context.Context, userID uuid.UUID, kycLevel int32) error {
	return m.kycTierRepo.Apply(ctx, userID, kycLevel)
}

// InternalRouter returns the HTTP handler meant for the internal-only
// listener — every registered transaction type is postable through it,
// including money movement to/from system accounts (money_in, refund,
// withdraw settlement, escrow release, fee_collect). The caller MUST NOT
// expose this to untrusted networks (docs/plan/10 Task T1).
func (m *Module) InternalRouter() http.Handler {
	return transport.NewInternalRouterWithFeePolicy(m, m.feePolicy)
}

// LoadCurrencies loads the `currencies` table into pkg/currency's runtime
// registry (docs/plan/18 Task T1) — call once at startup, BEFORE serving
// traffic, right after NewModule. Deliberately a separate call rather than
// happening inside NewModule itself: NewModule has no context.Context or
// error return, and every other startup dependency (Postgres, Redis,
// RabbitMQ) in the composition root already follows the same
// connect-then-explicitly-check-error-then-os.Exit(1) shape — this keeps
// currency loading consistent with that pattern instead of a special case.
// An error here should be fatal: an empty or wrong currency registry
// silently rejecting or accepting everything is worse than refusing to
// start.
func (m *Module) LoadCurrencies(ctx context.Context) error {
	list, err := m.currencyRepo.ListEnabled(ctx)
	if err != nil {
		return fmt.Errorf("load currencies: %w", err)
	}
	if len(list) == 0 {
		return fmt.Errorf("load currencies: no enabled currencies found in the currencies table")
	}
	currency.Load(list)
	return nil
}

// ledgerEventsQueue is a durable catch-all queue bound to the broker's
// default exchange (RABBITMQ_EXCHANGE, e.g. "ledger.events") with routing
// key "#". Declaring it is what makes the exchange itself exist — without
// it, publishing from the outbox relay fails at the broker (AMQP closes the
// channel when publishing to an undeclared exchange). No module consumes it
// yet; future modules (notifications, audit log) can bind their own queues
// to the same exchange with narrower routing keys.
const ledgerEventsQueue = "ledger.events.audit"

// StartWorkers declares the ledger event topology, then launches the outbox
// relay and the integrity verifier. No-op if WorkerConfig.Enabled is false.
// Call StopWorkers on shutdown.
func (m *Module) StartWorkers(ctx context.Context) {
	if !m.workerCfg.Enabled {
		m.logger.Info("ledger: workers disabled (WORKER_ENABLED=false)")
		return
	}
	if err := m.broker.DeclareTopology(ctx, messaging.QueueConfig{
		Queue:       ledgerEventsQueue,
		RoutingKeys: []string{"#"},
	}); err != nil {
		m.logger.Error("ledger: failed to declare event topology", slog.Any("error", err))
	}
	m.outboxRelay.Start(ctx)
	if err := m.verifier.Start(); err != nil {
		m.logger.Error("ledger: failed to start verifier", slog.Any("error", err))
	}
	if err := m.snapshotJob.Start(ctx); err != nil {
		m.logger.Error("ledger: failed to start balance snapshot job", slog.Any("error", err))
	}
	if err := m.scheduleJob.Start(ctx); err != nil {
		m.logger.Error("ledger: failed to start schedule runner job", slog.Any("error", err))
	}
	if err := m.accrualJob.Start(ctx); err != nil {
		m.logger.Error("ledger: failed to start interest accrual job", slog.Any("error", err))
	}
}

// StopWorkers gracefully stops the outbox relay and verifier, waiting for
// any in-flight batch/check to finish. Safe to call even if StartWorkers was
// never called or workers were disabled.
func (m *Module) StopWorkers() {
	if !m.workerCfg.Enabled {
		return
	}
	m.outboxRelay.Stop()
	m.verifier.Stop()
	m.snapshotJob.Stop()
	m.scheduleJob.Stop()
	m.accrualJob.Stop()
}

// Post submits a ledger command to the posting engine.
func (m *Module) Post(ctx context.Context, cmd Command) error {
	return m.handleSvc.Handle(ctx, cmd)
}

// RegisterGRPC exposes the service-facing ledger contract on s.
func (m *Module) RegisterGRPC(s *grpc.Server) {
	ledgerv1.RegisterLedgerServiceServer(s, grpcserver.New(m))
}

// ProvisionUser creates the standard account set for a new user. Idempotent.
func (m *Module) ProvisionUser(ctx context.Context, userID uuid.UUID, currency string) ([]Account, error) {
	return m.provisionSvc.CreateUserAccounts(ctx, userID, currency)
}

// CreatePocket creates a named pocket sub-account for a user. Idempotent.
func (m *Module) CreatePocket(ctx context.Context, userID uuid.UUID, currency, pocketCode string) (Account, error) {
	return m.provisionSvc.CreatePocket(ctx, userID, currency, pocketCode)
}

// ListAccounts returns every account owned by a user.
func (m *Module) ListAccounts(ctx context.Context, userID uuid.UUID) ([]Account, error) {
	return m.accountRepo.ListByOwner(ctx, userID)
}

// GetUserCurrency resolves the currency of a user's cash (or, if pocketCode
// is non-empty, pocket) account (docs/plan/18 Task T2) — used by the
// transport layer's fee policy to pick the right (type, gateway, currency)
// rule before an amount is validated against a specific account.
func (m *Module) GetUserCurrency(ctx context.Context, userID uuid.UUID, pocketCode string) (string, error) {
	var accID uuid.UUID
	var err error
	if pocketCode != "" {
		accID, err = m.accountRepo.GetPocketAccountID(ctx, userID, pocketCode)
	} else {
		accID, err = m.accountRepo.GetAccountID(ctx, userID, constant.AccountTypeCash)
	}
	if err != nil {
		return "", err
	}
	return m.accountRepo.GetAccountCurrency(ctx, accID)
}

// GetBalance returns the current balance for an account.
func (m *Module) GetBalance(ctx context.Context, accountID uuid.UUID) (Balance, error) {
	return m.balanceRepo.GetBalance(ctx, accountID)
}

// GetBalanceAsOf returns accountID's balance at the end of a past calendar
// day (Asia/Jakarta), computed from the nearest daily snapshot at or before
// that date plus the net delta of entries since — two lightweight queries,
// never a full replay of the account's history (docs/plan/15 Task T1).
// Currency/status/type/allow_negative always reflect the CURRENT account
// state — only Balance is historical.
func (m *Module) GetBalanceAsOf(ctx context.Context, accountID uuid.UUID, asOf time.Time) (Balance, error) {
	current, err := m.balanceRepo.GetBalance(ctx, accountID)
	if err != nil {
		return Balance{}, err
	}
	historical, err := m.snapshotRepo.BalanceAsOf(ctx, accountID, asOf)
	if err != nil {
		return Balance{}, err
	}
	current.Balance = historical
	return current, nil
}

// maxStatementEntries caps a single statement response (docs/plan/15 Task
// T2, decision K7) — a request whose period contains more entries than this
// is rejected with apperror.ErrStatementRangeTooLarge rather than silently
// truncated (a statement quietly missing entries is a financial bug, not a
// UX nicety).
const maxStatementEntries = 5000

// Statement returns accountID's opening balance, closing balance, and every
// ledger entry within [from, to] (Asia/Jakarta calendar days, both
// inclusive) — docs/plan/15 Task T2. OpeningBalance comes from
// GetBalanceAsOf(from - 1 day), never a full replay of the account's entire
// history.
func (m *Module) Statement(ctx context.Context, accountID uuid.UUID, from, to time.Time) (Statement, error) {
	bal, err := m.balanceRepo.GetBalance(ctx, accountID)
	if err != nil {
		return Statement{}, err
	}

	opening, err := m.snapshotRepo.BalanceAsOf(ctx, accountID, from.AddDate(0, 0, -1))
	if err != nil {
		return Statement{}, fmt.Errorf("statement: opening balance: %w", err)
	}

	entries, err := m.entryRepo.ListByAccountRange(ctx, accountID, from, to, m.loc, maxStatementEntries+1)
	if err != nil {
		return Statement{}, fmt.Errorf("statement: entries: %w", err)
	}
	if len(entries) > maxStatementEntries {
		return Statement{}, fmt.Errorf("%w: more than %d entries in range, narrow the period",
			apperror.ErrStatementRangeTooLarge, maxStatementEntries)
	}

	closing := opening
	if len(entries) > 0 {
		closing = entries[len(entries)-1].BalanceAfter
	}

	return Statement{
		AccountID: accountID, Currency: bal.Currency, From: from, To: to,
		OpeningBalance: opening, ClosingBalance: closing, Entries: entries,
	}, nil
}

// GetTransaction returns a transaction header by ID.
func (m *Module) GetTransaction(ctx context.Context, txID uuid.UUID) (Transaction, error) {
	return m.txRepo.GetByID(ctx, txID)
}

// GetTransactionByIdempotencyKey returns a transaction header by its
// idempotency key + scope — the way an external orchestrator (e.g.
// internal/payout, docs/plan/23 Task T3) recovers the tx ID Post() itself
// doesn't return, so it can later pass that ID as ReferenceID to a
// lifecycle-closing command (withdraw_settle/withdraw_cancel). scope=""
// means no scope (NULL), matching Command.IdempotencyScope's own
// empty-means-unscoped convention.
func (m *Module) GetTransactionByIdempotencyKey(ctx context.Context, key, scope string) (Transaction, error) {
	var scopePtr *string
	if scope != "" {
		scopePtr = &scope
	}
	return m.txRepo.GetByIdempotencyKey(ctx, key, scopePtr)
}

// ListEntries returns an account's ledger entries, newest first, using
// keyset pagination.
func (m *Module) ListEntries(ctx context.Context, accountID uuid.UUID, beforeCreatedAt time.Time, beforeID uuid.UUID, limit int) ([]Entry, error) {
	return m.entryRepo.ListByAccount(ctx, accountID, beforeCreatedAt, beforeID, limit)
}

// CanAccessAccount reports whether userID owns accountID.
func (m *Module) CanAccessAccount(ctx context.Context, accountID, userID uuid.UUID) (bool, error) {
	ownerID, err := m.accountRepo.GetOwnerID(ctx, accountID)
	if err != nil {
		return false, err
	}
	return ownerID == userID, nil
}

// CanAccessTransaction reports whether userID owns at least one account
// touched by the transaction.
//
// NOTE: this walks every account the transaction touched via GetAccountIDs
// rather than trusting ledger_transactions.source/destination_account_id,
// which are not reliably semantic for multi-account transactions (see
// docs/plan/04 note on D1, and the Phase 2 fix tracked as Task H6).
func (m *Module) CanAccessTransaction(ctx context.Context, txID, userID uuid.UUID) (bool, error) {
	accountIDs, err := m.txRepo.GetAccountIDs(ctx, txID)
	if err != nil {
		return false, err
	}
	for _, accID := range accountIDs {
		ownerID, err := m.accountRepo.GetOwnerID(ctx, accID)
		if err != nil {
			continue
		}
		if ownerID == userID {
			return true, nil
		}
	}
	return false, nil
}

// ReplayDeadEvent resets one dead-lettered outbox event back to 'failed'
// with a clean retry budget, so the relay's normal retry path picks it up
// on the next tick (docs/plan/12 Task T3). Returns
// apperror.ErrOutboxEventNotFound if id doesn't exist or isn't currently
// 'dead'. Only reachable via the internal router, admin-gated.
func (m *Module) ReplayDeadEvent(ctx context.Context, id uuid.UUID) error {
	return m.outboxRepo.ReplayDead(ctx, id)
}

// ListDeadOutboxEvents returns dead-lettered outbox events oldest first,
// paginated (docs/plan/25 Task T5) — lets an operator see what needs
// replay without querying Postgres directly.
func (m *Module) ListDeadOutboxEvents(ctx context.Context, limit, offset int) ([]DeadOutboxEvent, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	return m.outboxRepo.ListDead(ctx, limit, offset)
}

// ReplayDeadEvents replays every dead-lettered event created before
// olderThan, capped at 100 per call — call again (with the same or a later
// olderThan) to replay more. Returns the number actually replayed.
func (m *Module) ReplayDeadEvents(ctx context.Context, olderThan time.Time) (int, error) {
	return m.outboxRepo.ReplayAllDead(ctx, olderThan)
}

// CreateAdjustment requests a manual balance adjustment — it does NOT move
// any money, only records the request for a second identity to approve
// (docs/plan/16 Task T1, decision K8). adjType must be "adjustment_credit"
// or "adjustment_debit".
func (m *Module) CreateAdjustment(ctx context.Context, requestedBy, adjType string, amount decimal.Decimal, targetUserID uuid.UUID, metadata map[string]any, reason string) (uuid.UUID, error) {
	return m.adjustmentsSvc.Create(ctx, requestedBy, adjType, amount, targetUserID, metadata, reason)
}

// ApproveAdjustment authorizes and executes a pending adjustment. Returns
// the posted transaction id. approverID must differ from the original
// requester — enforced here and at the database level.
func (m *Module) ApproveAdjustment(ctx context.Context, id uuid.UUID, approverID string) (uuid.UUID, error) {
	return m.adjustmentsSvc.Approve(ctx, id, approverID)
}

// RejectAdjustment declines a pending adjustment — no money moves.
func (m *Module) RejectAdjustment(ctx context.Context, id uuid.UUID, approverID string) error {
	return m.adjustmentsSvc.Reject(ctx, id, approverID)
}

// GetAdjustment returns one pending adjustment by id.
func (m *Module) GetAdjustment(ctx context.Context, id uuid.UUID) (PendingAdjustment, error) {
	return m.adjustmentsSvc.Get(ctx, id)
}

// ListAdjustments returns pending adjustments filtered by status (empty =
// all), newest first.
func (m *Module) ListAdjustments(ctx context.Context, status string, limit int) ([]PendingAdjustment, error) {
	return m.adjustmentsSvc.List(ctx, status, limit)
}

// ImportReconBatch validates, persists, and matches one settlement report
// against the internal ledger in a single DB transaction (docs/plan/16 Task
// T2). Returns the created batch id.
func (m *Module) ImportReconBatch(ctx context.Context, gateway string, reportDate time.Time, filename string, rows []ReconImportRow, createdBy string) (uuid.UUID, error) {
	return m.reconSvc.ImportBatch(ctx, gateway, reportDate, filename, rows, createdBy)
}

// GetReconBatchReport returns a batch's header, a count per match_status,
// and a page of items — optionally filtered to one match_status.
func (m *Module) GetReconBatchReport(ctx context.Context, batchID uuid.UUID, matchStatus string, limit, offset int) (ReconBatchReport, error) {
	return m.reconSvc.GetBatchReport(ctx, batchID, matchStatus, limit, offset)
}

// ListReconBatches returns imported settlement batches newest first,
// paginated (docs/plan/25 Task T5) — lets an operator find a batch's id
// without SQL before drilling into GetReconBatchReport.
func (m *Module) ListReconBatches(ctx context.Context, limit, offset int) ([]ReconBatch, error) {
	return m.reconSvc.ListBatches(ctx, limit, offset)
}

// ResolveReconItem requests a correction for a non-matched recon item — it
// does NOT move any money, only creates a pending adjustment a second
// identity must separately approve (docs/plan/16 Task T2, decision K5).
// adjType must be "adjustment_suspense_credit" or "adjustment_suspense_debit".
func (m *Module) ResolveReconItem(ctx context.Context, itemID uuid.UUID, requestedBy, adjType string, amount decimal.Decimal, reason string) (uuid.UUID, error) {
	return m.reconSvc.ResolveItem(ctx, itemID, requestedBy, adjType, amount, reason)
}

// CreateSchedule stores a recurring/deferred user transaction request — it
// does NOT post anything (docs/plan/19 Task T1); the daily schedule runner
// (or the admin RunSchedulesNow endpoint) executes it once due.
func (m *Module) CreateSchedule(
	ctx context.Context, userID uuid.UUID, txType string, amount decimal.Decimal,
	targetUserID uuid.UUID, pocketCode string, metadata map[string]any,
	kind string, runAtDate time.Time, dayOfMonth *int, createdBy string,
) (uuid.UUID, error) {
	return m.scheduleSvc.Create(ctx, userID, txType, amount, targetUserID, pocketCode, metadata, kind, runAtDate, dayOfMonth, createdBy)
}

// ListSchedules returns userID's own scheduled transactions.
func (m *Module) ListSchedules(ctx context.Context, userID uuid.UUID) ([]ScheduledTransaction, error) {
	return m.scheduleSvc.List(ctx, userID)
}

// PauseSchedule/ResumeSchedule/CancelSchedule each require the caller to own
// the schedule — enforced in internal/ledger/service/schedule.
func (m *Module) PauseSchedule(ctx context.Context, id, userID uuid.UUID) error {
	return m.scheduleSvc.Pause(ctx, id, userID)
}

func (m *Module) ResumeSchedule(ctx context.Context, id, userID uuid.UUID) error {
	return m.scheduleSvc.Resume(ctx, id, userID)
}

func (m *Module) CancelSchedule(ctx context.Context, id, userID uuid.UUID) error {
	return m.scheduleSvc.Cancel(ctx, id, userID)
}

// RunSchedulesNow executes the schedule runner for a given date immediately,
// outside the cron schedule — internal-router-only, admin-gated ops/testing
// endpoint (docs/plan/19 Task T1 step 5).
func (m *Module) RunSchedulesNow(ctx context.Context, asOf time.Time) (executed, failed int, err error) {
	return m.scheduleJob.RunNow(ctx, asOf)
}

// ImportDisbursementBatch validates and persists a new batch — it does NOT
// post anything (docs/plan/19 Task T2); call RunDisbursement to start (or
// resume) processing.
func (m *Module) ImportDisbursementBatch(ctx context.Context, filename string, rows []DisbursementImportRow, createdBy string) (uuid.UUID, error) {
	return m.disbursementSvc.Import(ctx, filename, rows, createdBy)
}

// RunDisbursement processes up to 500 items still needing a Post attempt —
// call repeatedly until Done is true. There is no separate "resume"
// endpoint: calling this again after a partial run IS resuming, since an
// already-'posted' item is never reselected (docs/plan/19 Task T2).
func (m *Module) RunDisbursement(ctx context.Context, batchID uuid.UUID, retryFailed bool) (DisbursementRunResult, error) {
	return m.disbursementSvc.Run(ctx, batchID, retryFailed)
}

// GetDisbursementReport returns a batch's header, a count per item status,
// and a page of items — optionally filtered to one status.
func (m *Module) GetDisbursementReport(ctx context.Context, batchID uuid.UUID, status string, limit, offset int) (DisbursementBatchReport, error) {
	return m.disbursementSvc.GetReport(ctx, batchID, status, limit, offset)
}

// SetSavingsConfig registers (or re-registers) an account as
// interest-bearing (docs/plan/19 Task T3).
func (m *Module) SetSavingsConfig(ctx context.Context, accountID uuid.UUID, annualRateBps int, enabled bool) error {
	return m.accrualSvc.SetConfig(ctx, accountID, annualRateBps, enabled)
}

// ListSavingsConfigs returns every registered savings account (enabled or not).
func (m *Module) ListSavingsConfigs(ctx context.Context) ([]SavingsConfig, error) {
	return m.accrualSvc.ListConfigs(ctx)
}

// GetDailyPositionReport/GetDailyMutationReport/GetReconSummaryReport read
// the three regulatory-reporting views (docs/plan/20 Task T2,
// migrations/000018) — read-only, no new job/scheduler, pulled on demand.
func (m *Module) GetDailyPositionReport(ctx context.Context, from, to time.Time) ([]ReportDailyPosition, error) {
	return m.reportingRepo.DailyPosition(ctx, from, to)
}

func (m *Module) GetDailyMutationReport(ctx context.Context, from, to time.Time) ([]ReportDailyMutation, error) {
	return m.reportingRepo.DailyMutation(ctx, from, to)
}

func (m *Module) GetReconSummaryReport(ctx context.Context, from, to time.Time) ([]ReportReconSummary, error) {
	return m.reportingRepo.ReconSummary(ctx, from, to)
}
