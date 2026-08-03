// Package ledger is the public facade for the ledger module — a
// double-entry, append-only accounting engine with an idempotent,
// concurrency-safe posting pipeline and a transactional outbox for reliable
// event publishing.
//
// This is the ONLY package other modules or cmd/gateway may import from
// internal/ledger — importing any subpackage (repository, processors,
// service/*, transport) directly from outside the module is a boundary
// violation (docs/roadmap/archive/01-target-architecture.md).
package ledger

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"slices"
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
	"github.com/herdifirdausss/seev/internal/ledger/migration/balancev2"
	"github.com/herdifirdausss/seev/internal/ledger/processors"
	"github.com/herdifirdausss/seev/internal/ledger/repository"
	"github.com/herdifirdausss/seev/internal/ledger/service/accrual"
	"github.com/herdifirdausss/seev/internal/ledger/service/adjustments"
	"github.com/herdifirdausss/seev/internal/ledger/service/closure"
	"github.com/herdifirdausss/seev/internal/ledger/service/disbursement"
	"github.com/herdifirdausss/seev/internal/ledger/service/dispute"
	ledgerhandle "github.com/herdifirdausss/seev/internal/ledger/service/handle"
	interestservice "github.com/herdifirdausss/seev/internal/ledger/service/interest"
	"github.com/herdifirdausss/seev/internal/ledger/service/provision"
	"github.com/herdifirdausss/seev/internal/ledger/service/recon"
	"github.com/herdifirdausss/seev/internal/ledger/service/schedule"
	"github.com/herdifirdausss/seev/internal/ledger/transport"
	"github.com/herdifirdausss/seev/internal/ledger/worker"
	"github.com/herdifirdausss/seev/internal/migrationkit"
	"github.com/herdifirdausss/seev/pkg/alerting"
	"github.com/herdifirdausss/seev/pkg/cryptox"
	"github.com/herdifirdausss/seev/pkg/currency"
	"github.com/herdifirdausss/seev/pkg/database"
	"github.com/herdifirdausss/seev/pkg/fraudcheck"
	"github.com/herdifirdausss/seev/pkg/httpcontract"
	"github.com/herdifirdausss/seev/pkg/messaging"
	"github.com/herdifirdausss/seev/pkg/retentionworker"
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
	// PendingAdjustment is a maker-checker adjustment request (docs/roadmap/archive/16
	// Task T1).
	PendingAdjustment = model.PendingAdjustment
	// ReconBatchReport is an imported settlement batch's report (docs/roadmap/archive/16
	// Task T2).
	ReconBatchReport = model.ReconBatchReport
	ReconImportRow   = model.ReconImportRow
	// ReconBatch is one imported settlement batch header, listing-only
	// (docs/roadmap/archive/25 Task T5).
	ReconBatch = model.ReconBatch
	// DeadOutboxEvent is one dead-lettered outbox event, listing-only
	// (docs/roadmap/archive/25 Task T5).
	DeadOutboxEvent = model.DeadOutboxEvent
	// ScheduledTransaction is a recurring/deferred user transaction
	// (docs/roadmap/archive/19 Task T1).
	ScheduledTransaction = model.ScheduledTransaction
	// DisbursementImportRow/DisbursementBatchReport/DisbursementRunResult
	// back batch disbursement (docs/roadmap/archive/19 Task T2).
	DisbursementImportRow   = model.DisbursementImportRow
	DisbursementBatchReport = model.DisbursementBatchReport
	DisbursementRunResult   = model.DisbursementRunResult
	// SavingsConfig marks an account as interest-bearing (docs/roadmap/archive/19 Task T3).
	SavingsConfig = model.SavingsConfig
	SavingsProduct = model.SavingsProduct
	SavingsRateVersion = model.SavingsRateVersion
	SavingsEnrollment = model.SavingsEnrollment
	InterestPeriod = model.InterestPeriod
	InterestDailyAccrual = model.InterestDailyAccrual
	InterestCapitalizationItem = model.InterestCapitalizationItem
	InterestAdjustment = model.InterestAdjustment
	ScheduledOccurrence = model.ScheduledOccurrence
	ScheduledExecutionAttempt = model.ScheduledExecutionAttempt
	ScheduledPolicy = model.ScheduledPolicy
	InterestPeriodPreview = interestservice.PeriodPreview
	// ReportDailyPosition/ReportDailyMutation/ReportReconSummary back the
	// regulatory reporting endpoints (docs/roadmap/archive/20 Task T2).
	ReportDailyPosition = model.ReportDailyPosition
	ReportDailyMutation = model.ReportDailyMutation
	ReportReconSummary  = model.ReportReconSummary
	// PolicyChecker is satisfied structurally by internal/policy.Engine
	// (docs/roadmap/archive/17 Task T1) — re-exported so callers can name the type
	// without importing internal/ledger/transport; they never need to
	// import internal/policy either, since Go interface satisfaction is
	// structural (pass the concrete *policy.Engine value straight to
	// NewModule).
	PolicyChecker = transport.PolicyChecker
	// LedgerError is the structured error Post/Handle return for business
	// validation failures (never for infra errors) — re-exported so a
	// caller outside this module (e.g. internal/payin, docs/roadmap/archive/22 Task
	// T2) can classify "business failure, won't heal on retry" vs "infra
	// error, retry/redeliver" via errors.As(err, &ledgerErr), the same
	// pattern internal/ledger/service/schedule already uses internally.
	LedgerError = apperror.LedgerError
	// Quote is a fee quote row (docs/roadmap/archive/38) — re-exported so a caller
	// outside this module (e.g. internal/testutil's LedgerHarness, used by
	// internal/payout's own integration tests to create a quote to consume)
	// can name CreateQuote's return type without importing the
	// module-private internal/ledger/feepolicy package directly.
	Quote = feepolicy.Quote
	// ChargebackDispute is one card-network dispute case-management record
	// (business-completeness audit finding — migrations/ledger/000035_
	// chargeback_disputes), separate from the chargeback processor's own
	// money-movement transaction.
	ChargebackDispute = model.ChargebackDispute
	// ChargebackDisputeStatusChange is one row of a dispute's append-only
	// status-transition audit trail (security audit finding —
	// migrations/ledger/000037_chargeback_dispute_audit_trail).
	ChargebackDisputeStatusChange = model.ChargebackDisputeStatusChange
)

// ErrAlreadyClosed is returned by Post when a lifecycle-closing command
// (withdraw_settle, withdraw_cancel, reversal, ...) loses the atomic
// closed_by_tx_id race (docs/roadmap/archive/14 Task T2, decision K3) — re-exported so
// a caller outside this module (e.g. internal/payout, docs/roadmap/archive/23 Task T4)
// can distinguish "someone else already closed this" (re-read state,
// reconcile, not a real error) from every other business/infra failure via
// errors.Is(err, ledger.ErrAlreadyClosed). Payout deliberately does NOT
// build its own double-settle protection — this sentinel is how it detects
// that the ledger's own guard (the sole source of truth) already fired.
var ErrAlreadyClosed = apperror.ErrAlreadyClosed

// ErrQuoteExpired/ErrQuoteMismatch (docs/roadmap/archive/38 Task T5) are re-exported so
// a caller outside this module (payout, via ConsumeFeeQuote below) can
// classify a rejected quote consumption the same way any other business
// error is classified — errors.Is(err, ledger.ErrQuoteExpired) — without
// needing to import the module-private internal/ledger/feepolicy package.
var ErrQuoteExpired = apperror.ErrQuoteExpired
var ErrQuoteMismatch = apperror.ErrQuoteMismatch

// ErrTransactionNotFound/ErrAccountNotFound (Plan 57 T10 follow-up) are
// re-exported so a caller outside this module (internal/testutil's
// LedgerHarness, standing in for a real gRPC-connected ledgerclient.Client
// in internal/merchant/client's own integration tests) can classify
// GetMerchantAccount/GetMerchantTransaction's not-found cases via
// errors.Is, the same re-export pattern as ErrAlreadyClosed/ErrQuoteExpired
// above.
var ErrTransactionNotFound = apperror.ErrTransactionNotFound
var ErrAccountNotFound = apperror.ErrAccountNotFound

// WorkerConfig tunes the ledger module's background workers (outbox relay +
// integrity verifier). Deliberately independent of internal/config — the
// module must not depend on the composition root's config type.
type WorkerConfig struct {
	Enabled            bool
	// C5Enabled is an explicit activation switch for durable occurrences and
	// monthly interest workers. The new tables and operator controls can be
	// deployed before any product behavior is scheduled.
	C5Enabled          bool
	OutboxPollInterval time.Duration
	OutboxBatchSize    int
	// AlertWebhookURL, if non-empty, is POSTed to on every integrity
	// discrepancy the verifier finds (docs/roadmap/archive/12 Task T4). Empty = no
	// external alert, log+metric only (backward compatible default).
	AlertWebhookURL string
	BalanceV2       balancev2.Config
}

// Module is the public facade for the ledger module.
type Module struct {
	db              database.DatabaseSQL
	handleSvc       *ledgerhandle.Service
	provisionSvc    *provision.Service
	adjustmentsSvc  *adjustments.Service
	reconSvc        *recon.Service
	scheduleSvc     *schedule.Service
	durableScheduleSvc *schedule.DurableService
	disbursementSvc *disbursement.Service
	accrualSvc      *accrual.Service
	interestSvc     *interestservice.Service
	closureSvc      *closure.Service
	disputeSvc      *dispute.Service

	accountRepo      repository.AccountRepository
	balanceRepo      repository.BalanceRepository
	txRepo           repository.TransactionRepository
	entryRepo        repository.EntryRepository
	outboxRepo       repository.OutboxRepository
	snapshotRepo     repository.SnapshotRepository
	currencyRepo     repository.CurrencyRepository
	scheduleRepo     repository.ScheduledTransactionRepository
	scheduleOccurrenceRepo repository.ScheduledOccurrenceRepository
	disbursementRepo repository.DisbursementRepository
	savingsRepo      repository.SavingsRepository
	c5InterestRepo   repository.C5InterestRepository
	reportingRepo    repository.ReportingRepository
	kycTierRepo      repository.KycTierRepository

	router            http.Handler
	policyChecker     PolicyChecker
	feePolicy         *feepolicy.Policy
	processorRegistry *processors.ProcessorRegistry

	broker          messaging.Broker
	workerCfg       WorkerConfig
	outboxRelay     *worker.OutboxRelay
	verifier        *worker.Verifier
	balanceV2       *balancev2.Runtime
	snapshotJob     *worker.SnapshotJob
	scheduleJob     *worker.ScheduleRunnerJob
	accrualJob      *worker.AccrualJob
	c5Job           *worker.C5FinancialProductJob
	retentionRunner *retentionworker.Runner
	retentionSched  *scheduler.Scheduler
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
// posted transaction — zero/negative disables it (docs/roadmap/archive/10 Task T5).
//
// policyChecker, if non-nil, is evaluated before every posting on the
// PUBLIC router only (docs/roadmap/archive/17 Task T1) — the internal router never
// applies it (trusted internal callers aren't subject to end-user velocity
// limits). Pass nil to disable policy checks entirely — byte-identical
// behavior to before this parameter existed.
//
// fraudClient, if non-nil, screens every PUBLIC-router posting BEFORE any
// DB transaction opens (docs/roadmap/archive/37) — nil disables screening entirely,
// same convention as policyChecker. This replaced the old in-transaction
// PrePostHook seam (docs/roadmap/archive/20): screening moved out of
// internal/ledger/service/handle entirely, into the transport layer, so no
// network round-trip ever happens while a row lock is held.
// digestRing is docs/roadmap/archive/51-a8-data-lifecycle-privacy.md T3's (K7) idempotency-key
// digest ring, REQUIRED (never nil, unlike every T2 field-encryption ring
// used to be). cryptoxRing is now ALSO required — "A8 T2.5b" (the contract
// migration) removed recon_batches.source_filename/recon_items.raw's
// plaintext fallback, so repository.NewReconRepository panics on a nil
// ring; there is no longer a valid "cryptox unconfigured" mode to
// construct this module in.
func NewModule(db database.DatabaseSQL, broker messaging.Broker, redisClient *redis.Client, workerCfg WorkerConfig, logger *slog.Logger, maxAmountPerTx decimal.Decimal, policyChecker PolicyChecker, fraudClient *fraudcheck.Client, feeQuoteTTL time.Duration, digestRing *cryptox.DigestRing, cryptoxRing *cryptox.Ring) *Module {
	if logger == nil {
		logger = slog.Default()
	}

	accountRepo := repository.NewAccountRepository(db)
	txRepo := repository.NewTransactionRepository(db, digestRing)
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
	reconRepo := repository.NewReconRepository(db, cryptoxRing)
	currencyRepo := repository.NewCurrencyRepository(db)
	scheduleRepo := repository.NewScheduledTransactionRepository(db)
	scheduleOccurrenceRepo := repository.NewScheduledOccurrenceRepository(db)
	disbursementRepo := repository.NewDisbursementRepository(db)
	savingsRepo := repository.NewSavingsRepository(db)
	c5InterestRepo := repository.NewC5InterestRepository(db)
	reportingRepo := repository.NewReportingRepository(db)
	kycTierRepo := repository.NewKycTierRepository(db)
	disputeRepo := repository.NewChargebackDisputeRepository(db)

	feeRepo := repository.NewFeeRepository(db)
	// FEE_RESOLVER_CACHE_TTL is a B3 load-test-experiment escape hatch
	// (docs/performance/reports/2026-xx-baseline.md §22), not product
	// configuration — deliberately read directly via os.Getenv rather than
	// threaded through internal/config or NewModule's own (already long)
	// parameter list, so it stays invisible to every normal deployment path
	// and every existing NewModule call site. Absent or invalid = today's
	// unchanged behavior (feepolicy.Policy's own doc comment: "no
	// process-local cache: admin changes take effect on the next request").
	if raw := os.Getenv("FEE_RESOLVER_CACHE_TTL"); raw != "" {
		if ttl, parseErr := time.ParseDuration(raw); parseErr == nil && ttl > 0 {
			feeRepo = feepolicy.NewCachingFeeRepository(feeRepo, ttl)
		}
	}
	feeQuotePolicy := feepolicy.New(db, feeRepo)
	handleSvc := ledgerhandle.New(db, txRepo, balanceRepo, entryRepo, outboxRepo, registry, logger, maxAmountPerTx, feeQuotePolicy)
	balanceV2Runtime := balancev2.NewRuntime(db, workerCfg.BalanceV2, logger)
	handleSvc.SetProjectionV2Writer(balanceV2Runtime)
	adjustmentsSvc := adjustments.New(db, adjRepo, txRepo, outboxRepo, handleSvc)
	scheduleSvc := schedule.New(db, scheduleRepo, handleSvc, logger)
	durableScheduleSvc := schedule.NewDurable(scheduleRepo, scheduleOccurrenceRepo, handleSvc, feeQuotePolicy, logger, loc)
	durableScheduleSvc.SetDatabase(db)
	durableScheduleSvc.SetOutbox(outboxRepo)
	if workerCfg.C5Enabled {
		scheduleSvc.SetDurable(durableScheduleSvc)
	}
	disbursementSvc := disbursement.New(db, disbursementRepo, txRepo, handleSvc)
	accrualSvc := accrual.New(db, savingsRepo, snapshotRepo, handleSvc, logger)
	interestSvc := interestservice.New(db, c5InterestRepo, snapshotRepo, handleSvc, logger, loc)
	interestSvc.SetOutbox(outboxRepo)

	m := &Module{
		db:                db,
		handleSvc:         handleSvc,
		provisionSvc:      provision.New(db, repository.NewProvisioningRepository()),
		adjustmentsSvc:    adjustmentsSvc,
		reconSvc:          recon.New(db, reconRepo, adjustmentsSvc),
		scheduleSvc:       scheduleSvc,
		durableScheduleSvc: durableScheduleSvc,
		disbursementSvc:   disbursementSvc,
		accrualSvc:        accrualSvc,
		interestSvc:       interestSvc,
		closureSvc:        closure.New(db),
		disputeSvc:        dispute.New(disputeRepo, txRepo),
		accountRepo:       accountRepo,
		balanceRepo:       balanceRepo,
		txRepo:            txRepo,
		entryRepo:         entryRepo,
		outboxRepo:        outboxRepo,
		snapshotRepo:      snapshotRepo,
		currencyRepo:      currencyRepo,
		scheduleRepo:      scheduleRepo,
		scheduleOccurrenceRepo: scheduleOccurrenceRepo,
		disbursementRepo:  disbursementRepo,
		savingsRepo:       savingsRepo,
		c5InterestRepo:    c5InterestRepo,
		reportingRepo:     reportingRepo,
		kycTierRepo:       kycTierRepo,
		broker:            broker,
		workerCfg:         workerCfg,
		loc:               loc,
		logger:            logger,
		processorRegistry: registry,
		balanceV2:         balanceV2Runtime,
	}
	interestSvc.SetTransactionLookup(m)
	durableScheduleSvc.SetTransactionLookup(m)
	m.provisionSvc.SetProjectionV2Writer(balanceV2Runtime)
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
	m.c5Job = worker.NewC5FinancialProductJob(m, lock, logger, loc)

	// docs/roadmap/archive/51-a8-data-lifecycle-privacy.md T1: this module's own retention
	// classes (config/data-retention.yaml). One dedicated scheduler, matching
	// this constructor's own per-job-group convention above (verifier,
	// snapshot, schedule, accrual each get their own too) rather than
	// sharing one of theirs.
	retentionRunner, err := retentionworker.NewRunner("ledger", db, []retentionworker.Class{
		{Name: "ledger.fee_quotes.unconsumed", Action: "delete", FunctionName: "fn_retention_purge_fee_quotes_unconsumed"},
		{Name: "ledger.fee_quotes.consumed", Action: "delete", FunctionName: "fn_retention_purge_fee_quotes_consumed"},
		{Name: "ledger.outbox_events.published", Action: "delete", FunctionName: "fn_retention_purge_outbox_events_published"},
		// docs/roadmap/archive/51 T2.6: redacts recon_batches.source_filename /
		// recon_items.raw (both plaintext AND T2.4 ciphertext columns)
		// without ever decrypting them.
		{Name: "ledger.recon_batches", Action: "redact", FunctionName: "fn_retention_purge_recon_batches"},
		{Name: "ledger.recon_items", Action: "redact", FunctionName: "fn_retention_purge_recon_items"},
		// docs/roadmap/archive/51 T3 (K7): nulls idempotency_key/idempotency_scope 30
		// days after a transaction reaches a terminal status — the
		// SECURITY DEFINER function's own idempotency_key_digest IS NOT
		// NULL guard is what makes this safe (never redacts a row whose
		// permanent digest hasn't been backfilled yet).
		{Name: "ledger.transactions.idempotency_raw", Action: "redact", FunctionName: "fn_retention_purge_transactions_idempotency_raw"},
		{Name: "ledger.scheduled_transactions", Action: "delete", FunctionName: "fn_retention_purge_scheduled_transactions"},
	})
	if err != nil {
		// Every Class above is a fixed literal this constructor controls —
		// an error here means a programming mistake (typo'd function name,
		// duplicate class), not a runtime condition. Fail loudly at boot
		// rather than silently run without retention.
		panic(fmt.Sprintf("ledger: invalid retention configuration: %v", err))
	}
	m.retentionRunner = retentionRunner
	m.retentionSched = scheduler.NewScheduler(lock, scheduler.NewPrometheusMetrics(), scheduler.WithLocation(loc))

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
// (docs/roadmap/archive/10 Task T1). The caller mounts it under a path prefix and wraps
// it with auth/rate-limit middleware.
func (m *Module) Router() http.Handler {
	return m.router
}

// ResolveFee prices a transaction for one user. ok=false means no enabled
// database rule matched (or the resolved rule would produce an invalid fee).
func (m *Module) ResolveFee(ctx context.Context, userID uuid.UUID, txType, gateway, currency string, amount decimal.Decimal) (fee decimal.Decimal, feeGateway string, ok bool) {
	return m.feePolicy.Resolve(ctx, userID, txType, gateway, currency, amount)
}

// CreateQuote prices and persists a single-use fee quote (docs/roadmap/archive/38 Task
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
// POST /fees/quote (docs/roadmap/archive/38 Task T5) — a short, standalone operation
// (no ledger posting tx involved), exposed over gRPC so payout-service (a
// separate process with no direct access to seev_ledger) can spend a quote
// before it holds funds. Rejection surfaces as *apperror.LedgerError (via
// apperror.ErrQuoteExpired/ErrQuoteMismatch — the same re-exported sentinels
// execTransfer's own quote consumption uses, docs/roadmap/archive/38 Task T4) rather
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

// ConsumeFeeQuoteWithGateway is the route-bound quote consumption seam used
// by C5 top-up intents. It preserves the public error classification of
// ConsumeFeeQuote while adding the provider gateway to the exact-match key.
func (m *Module) ConsumeFeeQuoteWithGateway(ctx context.Context, quoteID, userID uuid.UUID, txType, gateway, currency string, amount decimal.Decimal, ref string) (fee decimal.Decimal, feeGateway string, err error) {
	fee, feeGateway, err = m.feePolicy.ConsumeQuoteWithGatewayStandalone(ctx, quoteID, userID, txType, gateway, currency, amount, ref)
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
// policy_tier_limits template for kycLevel (docs/roadmap/archive/39 Task T5) — called
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
// expose this to untrusted networks (docs/roadmap/archive/10 Task T1).
func (m *Module) InternalRouter() http.Handler {
	return transport.NewInternalRouterWithFeePolicy(m, m.feePolicy)
}

// ListMigrations and the methods below back the typed internal migration
// control plane. The HTTP package consumes them through a small optional
// interface so ordinary Ledger service mocks do not inherit operator APIs.
func (m *Module) ListMigrations(ctx context.Context) ([]balancev2.Migration, error) {
	if err := m.ensureBalanceMigration(ctx); err != nil {
		return nil, err
	}
	items, err := m.balanceV2.Controls().List(ctx)
	if err != nil {
		return nil, err
	}
	filtered := make([]balancev2.Migration, 0, 1)
	for _, item := range items {
		if isBalanceMigration(item) {
			filtered = append(filtered, item)
		}
	}
	return filtered, nil
}

func (m *Module) GetMigration(ctx context.Context, id uuid.UUID) (balancev2.Migration, error) {
	if err := m.ensureBalanceMigration(ctx); err != nil {
		return balancev2.Migration{}, err
	}
	item, err := m.balanceV2.Controls().Get(ctx, id)
	if err != nil {
		return balancev2.Migration{}, err
	}
	if !isBalanceMigration(item) {
		return balancev2.Migration{}, balancev2.ErrMigrationNotFound
	}
	return item, nil
}

func isBalanceMigration(item balancev2.Migration) bool {
	return item.Name == balancev2.MigrationName && item.Resource == balancev2.Resource
}

func (m *Module) ensureBalanceMigration(ctx context.Context) error {
	return m.balanceV2.Initialize(ctx, "service:ledger-admin")
}

func (m *Module) TransitionMigration(ctx context.Context, id uuid.UUID, to, actor, approver, reason string, expectedVersion int64) (balancev2.Migration, error) {
	if err := m.ensureBalanceMigration(ctx); err != nil {
		return balancev2.Migration{}, err
	}
	current, err := m.balanceV2.Controls().Get(ctx, id)
	if err != nil {
		return balancev2.Migration{}, err
	}
	if !isBalanceMigration(current) {
		return balancev2.Migration{}, balancev2.ErrMigrationNotFound
	}
	gate := balancev2.GateSnapshot{Passed: true, FreshAt: time.Now().UTC(), Reason: "non-gated lifecycle transition"}
	if migrationkit.RequiresGate(migrationkit.State(to)) {
		gate, err = m.balanceV2.Controls().Gates(ctx, current)
		if err != nil {
			return balancev2.Migration{}, err
		}
	}
	result, transitionErr := m.balanceV2.Controls().Transition(ctx, balancev2.TransitionRequest{
		MigrationID: id, ToState: to, RequestedBy: actor, ApprovedBy: approver,
		Reason: reason, ExpectedVersion: expectedVersion,
	}, gate)
	if transitionErr == nil {
		m.balanceV2.Refresh()
	}
	return result, transitionErr
}

func (m *Module) SetMigrationReadPercentage(ctx context.Context, id uuid.UUID, percentage int, actor, approver, reason string, expectedVersion int64) (balancev2.Migration, error) {
	if err := m.ensureBalanceMigration(ctx); err != nil {
		return balancev2.Migration{}, err
	}
	migration, err := m.balanceV2.Controls().Get(ctx, id)
	if err != nil {
		return balancev2.Migration{}, err
	}
	if !isBalanceMigration(migration) {
		return balancev2.Migration{}, balancev2.ErrMigrationNotFound
	}
	gate := balancev2.GateSnapshot{Passed: true, FreshAt: time.Now().UTC(), Reason: "read percentage unchanged"}
	if percentage > migration.ReadPercentageBasisPoints {
		gate, err = m.balanceV2.Controls().Gates(ctx, migration)
		if err != nil {
			return balancev2.Migration{}, err
		}
	}
	result, setErr := m.balanceV2.Controls().SetReadPercentage(ctx, id, percentage, actor, approver, reason, expectedVersion, gate)
	if setErr == nil {
		m.balanceV2.Refresh()
	}
	return result, setErr
}

func (m *Module) SetMigrationDualWrite(ctx context.Context, id uuid.UUID, strict bool, actor, reason string, expectedVersion int64) (balancev2.Migration, error) {
	if err := m.ensureBalanceMigration(ctx); err != nil {
		return balancev2.Migration{}, err
	}
	current, err := m.balanceV2.Controls().Get(ctx, id)
	if err != nil {
		return balancev2.Migration{}, err
	}
	if !isBalanceMigration(current) {
		return balancev2.Migration{}, balancev2.ErrMigrationNotFound
	}
	result, setErr := m.balanceV2.Controls().SetDualWrite(ctx, id, strict, actor, reason, expectedVersion)
	if setErr == nil {
		m.balanceV2.Refresh()
	}
	return result, setErr
}

func (m *Module) PauseMigration(ctx context.Context, id uuid.UUID, actor, reason string, expectedVersion int64) (balancev2.Migration, error) {
	if err := m.ensureBalanceMigration(ctx); err != nil {
		return balancev2.Migration{}, err
	}
	current, err := m.balanceV2.Controls().Get(ctx, id)
	if err != nil {
		return balancev2.Migration{}, err
	}
	if !isBalanceMigration(current) {
		return balancev2.Migration{}, balancev2.ErrMigrationNotFound
	}
	result, pauseErr := m.balanceV2.Controls().Pause(ctx, id, actor, reason, expectedVersion)
	if pauseErr == nil {
		m.balanceV2.Refresh()
	}
	return result, pauseErr
}

func (m *Module) ResumeMigration(ctx context.Context, id uuid.UUID, actor, reason string, expectedVersion int64) (balancev2.Migration, error) {
	if err := m.ensureBalanceMigration(ctx); err != nil {
		return balancev2.Migration{}, err
	}
	current, err := m.balanceV2.Controls().Get(ctx, id)
	if err != nil {
		return balancev2.Migration{}, err
	}
	if !isBalanceMigration(current) {
		return balancev2.Migration{}, balancev2.ErrMigrationNotFound
	}
	result, resumeErr := m.balanceV2.Controls().Resume(ctx, id, actor, reason, expectedVersion)
	if resumeErr == nil {
		m.balanceV2.Refresh()
	}
	return result, resumeErr
}

func (m *Module) ListMigrationMismatches(ctx context.Context, id uuid.UUID, status string, limit, offset int) ([]balancev2.Mismatch, error) {
	if err := m.ensureBalanceMigration(ctx); err != nil {
		return nil, err
	}
	migration, err := m.balanceV2.Controls().Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if !isBalanceMigration(migration) {
		return nil, balancev2.ErrMigrationNotFound
	}
	return m.balanceV2.Controls().ListMismatches(ctx, id, status, limit, offset)
}

func (m *Module) RunMigrationPreCutoverReconciliation(ctx context.Context, id uuid.UUID, actor string, backupFresh bool) error {
	if err := m.ensureBalanceMigration(ctx); err != nil {
		return err
	}
	migration, err := m.balanceV2.Controls().Get(ctx, id)
	if err != nil {
		return err
	}
	if !isBalanceMigration(migration) {
		return balancev2.ErrMigrationNotFound
	}
	return m.balanceV2.RunPreCutoverReconciliation(ctx, actor, backupFresh)
}

func (m *Module) RequestMigrationRepair(ctx context.Context, migrationID, mismatchID uuid.UUID, actor, reason string) (balancev2.Repair, error) {
	if err := m.ensureBalanceMigration(ctx); err != nil {
		return balancev2.Repair{}, err
	}
	mismatch, err := m.balanceV2.Controls().GetMismatch(ctx, mismatchID)
	if err != nil {
		return balancev2.Repair{}, err
	}
	if mismatch.MigrationID != migrationID {
		return balancev2.Repair{}, balancev2.ErrMigrationNotFound
	}
	migration, err := m.balanceV2.Controls().Get(ctx, migrationID)
	if err != nil {
		return balancev2.Repair{}, err
	}
	if !isBalanceMigration(migration) {
		return balancev2.Repair{}, balancev2.ErrMigrationNotFound
	}
	return m.balanceV2.RequestRepair(ctx, mismatchID, actor, reason)
}

func (m *Module) ApproveMigrationRepair(ctx context.Context, migrationID, repairID, accountID uuid.UUID, approver, reason string) (balancev2.Repair, error) {
	if err := m.ensureBalanceMigration(ctx); err != nil {
		return balancev2.Repair{}, err
	}
	repair, err := m.balanceV2.Controls().GetRepair(ctx, repairID)
	if err != nil {
		return balancev2.Repair{}, err
	}
	if repair.MigrationID != migrationID {
		return balancev2.Repair{}, balancev2.ErrMigrationNotFound
	}
	migration, err := m.balanceV2.Controls().Get(ctx, migrationID)
	if err != nil {
		return balancev2.Repair{}, err
	}
	if !isBalanceMigration(migration) {
		return balancev2.Repair{}, balancev2.ErrMigrationNotFound
	}
	return m.balanceV2.ApproveRepair(ctx, repairID, accountID, approver, reason)
}

// ClosureRouter returns docs/roadmap/archive/51-a8-data-lifecycle-privacy.md T5/T4b's (K9, K10,
// K11) privacy endpoints for auth-service's cross-service closure and
// export sagas: POST /privacy/closure/prepare, POST /privacy/closure/commit,
// and GET /privacy/export. Deliberately a SEPARATE handler from InternalRouter/transport.Service
// rather than an addition to that existing, heavily-used interface — this
// is a narrowly-scoped, single-caller (auth-service only) feature, and
// widening transport.Service would force every other transport.Service
// implementer (there is only one today, but the interface is the module
// boundary) to grow two methods it will never use. The caller MUST wrap
// this in pkg/middleware.WithInternalToken and mount it only on the
// internal-only listener, same as InternalRouter.
func (m *Module) ClosureRouter() http.Handler {
	mux := httpcontract.New(httpcontract.Options{Owner: "ledger", Audience: "internal", Contract: "internal-v1"})
	mux.HandleFunc("POST /privacy/closure/prepare", m.handleClosurePrepare)
	mux.HandleFunc("POST /privacy/closure/commit", m.handleClosureCommit)
	// docs/roadmap/archive/51 T4b (K9): same router, same token gate — auth's
	// export saga's own owner-composed export contract for ledger.
	mux.HandleFunc("GET /privacy/export", m.handlePrivacyExport)
	return mux
}

// LoadCurrencies loads the `currencies` table into pkg/currency's runtime
// registry (docs/roadmap/archive/18 Task T1) — call once at startup, BEFORE serving
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
	if m.balanceV2 != nil && m.workerCfg.BalanceV2.Enabled {
		if err := m.balanceV2.Initialize(ctx, "service:ledger"); err != nil {
			m.logger.Error("ledger: failed to initialize balance migration reference", slog.Any("error", err))
		}
		m.balanceV2.Start(ctx)
	}
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
	if m.workerCfg.C5Enabled {
		if err := m.c5Job.Start(ctx); err != nil {
			m.logger.Error("ledger: failed to start C5 financial product job", slog.Any("error", err))
		}
	}
	if err := m.retentionRunner.Start(m.retentionSched); err != nil {
		m.logger.Error("ledger: failed to start data retention job", slog.Any("error", err))
	}
}

// StopWorkers gracefully stops the outbox relay and verifier, waiting for
// any in-flight batch/check to finish. Safe to call even if StartWorkers was
// never called or workers were disabled.
func (m *Module) StopWorkers() {
	if m.balanceV2 != nil {
		m.balanceV2.Stop()
	}
	if !m.workerCfg.Enabled {
		return
	}
	m.outboxRelay.Stop()
	m.verifier.Stop()
	m.snapshotJob.Stop()
	m.scheduleJob.Stop()
	m.accrualJob.Stop()
	if m.workerCfg.C5Enabled {
		m.c5Job.Stop()
	}
	m.retentionSched.Stop()
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
// is non-empty, pocket) account (docs/roadmap/archive/18 Task T2) — used by the
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
	if m.balanceV2 != nil {
		return m.balanceV2.ReadBalance(ctx, accountID, m.balanceRepo.GetBalance)
	}
	return m.balanceRepo.GetBalance(ctx, accountID)
}

// GetBalanceAsOf returns accountID's balance at the end of a past calendar
// day (Asia/Jakarta), computed from the nearest daily snapshot at or before
// that date plus the net delta of entries since — two lightweight queries,
// never a full replay of the account's history (docs/roadmap/archive/15 Task T1).
// Currency/status/type/allow_negative always reflect the CURRENT account
// state — only Balance is historical.
func (m *Module) GetBalanceAsOf(ctx context.Context, accountID uuid.UUID, asOf time.Time) (Balance, error) {
	current, err := m.GetBalance(ctx, accountID)
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

// maxStatementEntries caps a single statement response (docs/roadmap/archive/15 Task
// T2, decision K7) — a request whose period contains more entries than this
// is rejected with apperror.ErrStatementRangeTooLarge rather than silently
// truncated (a statement quietly missing entries is a financial bug, not a
// UX nicety).
const maxStatementEntries = 5000

// Statement returns accountID's opening balance, closing balance, and every
// ledger entry within [from, to] (Asia/Jakarta calendar days, both
// inclusive) — docs/roadmap/archive/15 Task T2. OpeningBalance comes from
// GetBalanceAsOf(from - 1 day), never a full replay of the account's entire
// history.
func (m *Module) Statement(ctx context.Context, accountID uuid.UUID, from, to time.Time) (Statement, error) {
	bal, err := m.GetBalance(ctx, accountID)
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
// internal/payout, docs/roadmap/archive/23 Task T3) recovers the tx ID Post() itself
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

// ProvisionMerchant creates the standard account set for a merchant
// tenant — cash (T5) and hold (T6, required for the merchant payout
// state machine). Idempotent; returns the cash account as the tenant's
// primary account id, matching the existing gRPC contract
// (ProvisionMerchantResponse.account_id).
func (m *Module) ProvisionMerchant(ctx context.Context, tenantID uuid.UUID, currency string) (Account, error) {
	if _, err := m.provisionSvc.ProvisionMerchantHoldAccount(ctx, tenantID, currency); err != nil {
		return Account{}, fmt.Errorf("provision merchant hold account: %w", err)
	}
	return m.provisionSvc.ProvisionMerchantAccount(ctx, tenantID, currency)
}

// GetMerchantAccount resolves a tenant's cash account and its current
// balance in one call — the account id is NEVER accepted from a caller,
// only ever resolved server-side from tenantID (Plan 57 T5). Balance
// already carries AccountID/Currency/Status, so a second ListByOwner-style
// lookup for the account row itself would be redundant.
func (m *Module) GetMerchantAccount(ctx context.Context, tenantID uuid.UUID) (Balance, error) {
	accountID, err := m.accountRepo.GetMerchantAccountID(ctx, tenantID, constant.AccountTypeCash)
	if err != nil {
		return Balance{}, err
	}
	return m.GetBalance(ctx, accountID)
}

// ListMerchantTransactions returns tenantID's own transactions (as either
// source or destination), newest first — the account id is resolved
// server-side from tenantID, never accepted as a parameter, so no caller
// can ever list another tenant's transactions (Plan 57 T5).
func (m *Module) ListMerchantTransactions(ctx context.Context, tenantID uuid.UUID, beforeCreatedAt time.Time, beforeID uuid.UUID, limit int) ([]Transaction, error) {
	accountID, err := m.accountRepo.GetMerchantAccountID(ctx, tenantID, constant.AccountTypeCash)
	if err != nil {
		return nil, err
	}
	return m.txRepo.ListByAccountEitherSide(ctx, accountID, beforeCreatedAt, beforeID, limit)
}

// GetMerchantTransaction resolves ONE of tenantID's own transactions by id
// (Plan 57 T10 follow-up — backs both the B2B GET /transactions/{id} and
// GET /transfers/{id} routes; a merchant transaction has no separate
// "transfer" resource, only a Type value on the same Transaction). Mirrors
// CanAccessTransaction's own "walk every account the transaction touched"
// approach, applied to the tenant's resolved cash account instead of a
// user id, rather than trusting Transaction's own
// source/destination_account_id fields alone (docs/roadmap/archive/04's
// D1 note on multi-account transactions). Returns
// apperror.ErrTransactionNotFound both for a genuinely missing id and for
// one that touches none of tenantID's accounts — §6.7's "never leak
// resource existence across tenants."
func (m *Module) GetMerchantTransaction(ctx context.Context, tenantID, txID uuid.UUID) (Transaction, error) {
	accountID, err := m.accountRepo.GetMerchantAccountID(ctx, tenantID, constant.AccountTypeCash)
	if err != nil {
		return Transaction{}, err
	}
	tx, err := m.txRepo.GetByID(ctx, txID)
	if err != nil {
		return Transaction{}, err
	}
	accountIDs, err := m.txRepo.GetAccountIDs(ctx, txID)
	if err != nil {
		return Transaction{}, err
	}
	if slices.Contains(accountIDs, accountID) {
		return tx, nil
	}
	return Transaction{}, fmt.Errorf("%w: transaction %s", apperror.ErrTransactionNotFound, txID)
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
// docs/roadmap/archive/04 note on D1, and the Phase 2 fix tracked as Task H6).
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
// on the next tick (docs/roadmap/archive/12 Task T3). Returns
// apperror.ErrOutboxEventNotFound if id doesn't exist or isn't currently
// 'dead'. Only reachable via the internal router, admin-gated.
func (m *Module) ReplayDeadEvent(ctx context.Context, id uuid.UUID) error {
	return m.outboxRepo.ReplayDead(ctx, id)
}

// ListDeadOutboxEvents returns dead-lettered outbox events oldest first,
// paginated (docs/roadmap/archive/25 Task T5) — lets an operator see what needs
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
// (docs/roadmap/archive/16 Task T1, decision K8). adjType must be one of
// adjustment_credit, adjustment_debit, adjustment_suspense_credit,
// adjustment_suspense_debit, reversal, chargeback, freeze_confiscate
// (security audit finding — the last three used to be directly postable
// with a single admin JWT). referenceID is required for reversal (the
// transaction being reversed) and ignored otherwise.
func (m *Module) CreateAdjustment(ctx context.Context, requestedBy, adjType string, amount decimal.Decimal, targetUserID, referenceID uuid.UUID, metadata map[string]any, reason string) (uuid.UUID, error) {
	return m.adjustmentsSvc.Create(ctx, requestedBy, adjType, amount, targetUserID, referenceID, metadata, reason)
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
// against the internal ledger in a single DB transaction (docs/roadmap/archive/16 Task
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
// paginated (docs/roadmap/archive/25 Task T5) — lets an operator find a batch's id
// without SQL before drilling into GetReconBatchReport.
func (m *Module) ListReconBatches(ctx context.Context, limit, offset int) ([]ReconBatch, error) {
	return m.reconSvc.ListBatches(ctx, limit, offset)
}

// ResolveReconItem requests a correction for a non-matched recon item — it
// does NOT move any money, only creates a pending adjustment a second
// identity must separately approve (docs/roadmap/archive/16 Task T2, decision K5).
// adjType must be "adjustment_suspense_credit" or "adjustment_suspense_debit".
func (m *Module) ResolveReconItem(ctx context.Context, itemID uuid.UUID, requestedBy, adjType string, amount decimal.Decimal, reason string) (uuid.UUID, error) {
	return m.reconSvc.ResolveItem(ctx, itemID, requestedBy, adjType, amount, reason)
}

// OpenChargebackDispute opens a new case-management record against a posted
// charge (business-completeness audit finding). It does NOT move any money
// — posting the `chargeback` transaction and calling LinkChargebackTx once
// it lands are separate ops steps.
func (m *Module) OpenChargebackDispute(ctx context.Context, originalTxID uuid.UUID, disputeRef, cardNetwork, reasonCode string,
	amount decimal.Decimal, currency string, evidenceDueAt *time.Time, createdBy string) (uuid.UUID, error) {
	return m.disputeSvc.OpenDispute(ctx, originalTxID, disputeRef, cardNetwork, reasonCode, amount, currency, evidenceDueAt, createdBy)
}

// GetChargebackDispute returns one case by id.
func (m *Module) GetChargebackDispute(ctx context.Context, id uuid.UUID) (ChargebackDispute, error) {
	return m.disputeSvc.GetDispute(ctx, id)
}

// GetChargebackDisputeByRef returns one case by its external dispute_ref —
// the idempotent lookup a card network's webhook/report retry uses.
func (m *Module) GetChargebackDisputeByRef(ctx context.Context, disputeRef string) (ChargebackDispute, error) {
	return m.disputeSvc.GetDisputeByRef(ctx, disputeRef)
}

// ListChargebackDisputesForTransaction returns every case opened against
// one charge — a charge can accumulate more than one over time
// (re-presentment, then a second dispute).
func (m *Module) ListChargebackDisputesForTransaction(ctx context.Context, originalTxID uuid.UUID) ([]ChargebackDispute, error) {
	return m.disputeSvc.ListDisputesForTransaction(ctx, originalTxID)
}

// ListOpenChargebackDisputes returns 'open'/'evidence_submitted' cases
// ordered by evidence deadline — the ops queue.
func (m *Module) ListOpenChargebackDisputes(ctx context.Context, limit, offset int) ([]ChargebackDispute, error) {
	return m.disputeSvc.ListOpenDisputes(ctx, limit, offset)
}

// SubmitChargebackDisputeEvidence records the ops team's evidence package
// reference and moves the case from 'open' to 'evidence_submitted'.
// changedBy is recorded in the case's status-change audit trail.
func (m *Module) SubmitChargebackDisputeEvidence(ctx context.Context, id uuid.UUID, evidenceRef, changedBy string) error {
	return m.disputeSvc.SubmitEvidence(ctx, id, evidenceRef, changedBy)
}

// ResolveChargebackDispute closes a case with the card network's final
// decision (status must be won/lost/expired). resolvedBy is recorded on the
// case itself and in the status-change audit trail (security audit
// finding: resolution previously recorded no actor at all).
func (m *Module) ResolveChargebackDispute(ctx context.Context, id uuid.UUID, status, resolvedBy, reason string) error {
	return m.disputeSvc.ResolveDispute(ctx, id, status, resolvedBy, reason)
}

// LinkChargebackDisputeTx records the `chargeback` processor's transaction
// id once its forced-debit money movement posts, closing the loop between
// the case and the actual funds pulled.
func (m *Module) LinkChargebackDisputeTx(ctx context.Context, id, chargebackTxID uuid.UUID) error {
	return m.disputeSvc.LinkChargebackTx(ctx, id, chargebackTxID)
}

// ListChargebackDisputeStatusChanges returns a case's full transition
// history, oldest first — the audit trail security audit finding fixed.
func (m *Module) ListChargebackDisputeStatusChanges(ctx context.Context, disputeID uuid.UUID) ([]ChargebackDisputeStatusChange, error) {
	return m.disputeSvc.ListStatusChanges(ctx, disputeID)
}

// CreateSchedule stores a recurring/deferred user transaction request — it
// does NOT post anything (docs/roadmap/archive/19 Task T1); the daily schedule runner
// (or the admin RunSchedulesNow endpoint) executes it once due.
func (m *Module) CreateSchedule(
	ctx context.Context, userID uuid.UUID, txType string, amount decimal.Decimal,
	targetUserID uuid.UUID, pocketCode string, metadata map[string]any,
	kind string, runAtDate time.Time, dayOfMonth *int, createdBy string,
) (uuid.UUID, error) {
	return m.scheduleSvc.Create(ctx, userID, txType, amount, targetUserID, pocketCode, metadata, kind, runAtDate, dayOfMonth, createdBy)
}

func (m *Module) CreateScheduleWithPolicy(
	ctx context.Context, userID uuid.UUID, txType string, amount decimal.Decimal,
	targetUserID uuid.UUID, pocketCode string, metadata map[string]any,
	kind string, runAtDate time.Time, dayOfMonth *int, createdBy string,
	policy ScheduledPolicy, currency, timezone, localTime string,
) (uuid.UUID, error) {
	return m.scheduleSvc.CreateWithPolicy(ctx, userID, txType, amount, targetUserID, pocketCode, metadata, kind, runAtDate, dayOfMonth, createdBy, policy, currency, timezone, localTime)
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
// endpoint (docs/roadmap/archive/19 Task T1 step 5).
func (m *Module) RunSchedulesNow(ctx context.Context, asOf time.Time) (executed, failed int, err error) {
	return m.scheduleJob.RunNow(ctx, asOf)
}

// PlanScheduledOccurrences materializes C5 schedule occurrences for a
// calendar cutoff without executing them.
func (m *Module) PlanScheduledOccurrences(ctx context.Context, scheduleID uuid.UUID, asOf time.Time) ([]ScheduledOccurrence, error) {
	return m.durableScheduleSvc.PlanSchedule(ctx, scheduleID, asOf)
}

// ExecuteScheduledOccurrence runs one durable occurrence through the normal
// Ledger posting core. The boolean is false when another worker already owns
// or completed the occurrence.
func (m *Module) ExecuteScheduledOccurrence(ctx context.Context, occurrenceID uuid.UUID, owner string) (bool, error) {
	return m.durableScheduleSvc.ExecuteOccurrence(ctx, occurrenceID, owner)
}

func (m *Module) ListScheduledOccurrences(ctx context.Context, scheduleID, userID uuid.UUID, limit, offset int) ([]ScheduledOccurrence, error) {
	return m.durableScheduleSvc.ListOccurrences(ctx, scheduleID, userID, limit, offset)
}

func (m *Module) GetScheduledOccurrence(ctx context.Context, occurrenceID, userID uuid.UUID) (ScheduledOccurrence, error) {
	return m.durableScheduleSvc.GetOccurrence(ctx, occurrenceID, userID)
}

func (m *Module) ListScheduledExecutionAttempts(ctx context.Context, occurrenceID uuid.UUID) ([]ScheduledExecutionAttempt, error) {
	return m.scheduleOccurrenceRepo.ListAttempts(ctx, occurrenceID)
}

func (m *Module) RetryScheduledOccurrence(ctx context.Context, occurrenceID uuid.UUID) error {
	return m.durableScheduleSvc.RetryOccurrence(ctx, occurrenceID)
}

func (m *Module) ConfirmScheduledFeeCap(ctx context.Context, scheduleID, userID uuid.UUID, maxFeeAmount int64) error {
	return m.durableScheduleSvc.ConfirmFeeCap(ctx, scheduleID, userID, maxFeeAmount)
}

// ImportDisbursementBatch validates and persists a new batch — it does NOT
// post anything (docs/roadmap/archive/19 Task T2); a SECOND identity must call
// ApproveDisbursementBatch before RunDisbursement will process any item
// (business-completeness audit finding — decision K8's own maker-checker
// posture, previously only applied to manual adjustments).
func (m *Module) ImportDisbursementBatch(ctx context.Context, filename string, rows []DisbursementImportRow, createdBy string) (uuid.UUID, error) {
	return m.disbursementSvc.Import(ctx, filename, rows, createdBy)
}

// ApproveDisbursementBatch authorizes a batch for processing. approverID
// must differ from the identity that called ImportDisbursementBatch.
func (m *Module) ApproveDisbursementBatch(ctx context.Context, batchID uuid.UUID, approverID string) error {
	return m.disbursementSvc.ApproveBatch(ctx, batchID, approverID)
}

// RejectDisbursementBatch declines a batch — no items are ever processed.
// approverID must differ from the requester, same as ApproveDisbursementBatch.
func (m *Module) RejectDisbursementBatch(ctx context.Context, batchID uuid.UUID, approverID, reason string) error {
	return m.disbursementSvc.RejectBatch(ctx, batchID, approverID, reason)
}

// RunDisbursement processes up to 500 items still needing a Post attempt —
// call repeatedly until Done is true. There is no separate "resume"
// endpoint: calling this again after a partial run IS resuming, since an
// already-'posted' item is never reselected (docs/roadmap/archive/19 Task T2). The
// batch must already be approved (ApproveDisbursementBatch) — it always
// rejects a still-pending or rejected batch.
func (m *Module) RunDisbursement(ctx context.Context, batchID uuid.UUID, retryFailed bool) (DisbursementRunResult, error) {
	return m.disbursementSvc.Run(ctx, batchID, retryFailed)
}

// GetDisbursementReport returns a batch's header, a count per item status,
// and a page of items — optionally filtered to one status.
func (m *Module) GetDisbursementReport(ctx context.Context, batchID uuid.UUID, status string, limit, offset int) (DisbursementBatchReport, error) {
	return m.disbursementSvc.GetReport(ctx, batchID, status, limit, offset)
}

// SetSavingsConfig registers (or re-registers) an account as
// interest-bearing (docs/roadmap/archive/19 Task T3).
func (m *Module) SetSavingsConfig(ctx context.Context, accountID uuid.UUID, annualRateBps int, enabled bool) error {
	return m.accrualSvc.SetConfig(ctx, accountID, annualRateBps, enabled)
}

// ListSavingsConfigs returns every registered savings account (enabled or not).
func (m *Module) ListSavingsConfigs(ctx context.Context) ([]SavingsConfig, error) {
	return m.accrualSvc.ListConfigs(ctx)
}

// C5 savings-product and period-close facade. These operations remain on
// LedgerService; no separate financial-product application service is created.
func (m *Module) CreateSavingsProduct(ctx context.Context, product SavingsProduct) (SavingsProduct, error) {
	if product.Currency == "" || product.ProductCode == "" || product.Name == "" || product.CreatedBy == "" {
		return SavingsProduct{}, fmt.Errorf("%w: product code, name, currency, and created_by are required", apperror.ErrValidation)
	}
	if product.Status != "" && product.Status != model.SavingsProductDraft {
		return SavingsProduct{}, fmt.Errorf("%w: savings products must be created in draft status", apperror.ErrValidation)
	}
	product.Status = model.SavingsProductDraft
	if !currency.IsValid(product.Currency) {
		return SavingsProduct{}, fmt.Errorf("%w: unsupported savings product currency", apperror.ErrValidation)
	}
	if len(product.EligibleAccountTypes) > 0 {
		for _, accountType := range product.EligibleAccountTypes {
			if accountType != constant.AccountTypeCash && accountType != constant.AccountTypePocket {
				return SavingsProduct{}, fmt.Errorf("%w: unsupported savings eligible account type %q", apperror.ErrValidation, accountType)
			}
		}
	}
	if product.InterestExpenseAccountID == uuid.Nil || product.InterestPayableAccountID == uuid.Nil {
		return SavingsProduct{}, fmt.Errorf("%w: product system accounts are required", apperror.ErrValidation)
	}
	expectedExpense, err := m.accountRepo.GetSystemAccountID(ctx, constant.AccountTypeInterestExpense, "", product.Currency)
	if err != nil {
		return SavingsProduct{}, fmt.Errorf("%w: interest expense account is unavailable: %v", apperror.ErrValidation, err)
	}
	expectedPayable, err := m.accountRepo.GetSystemAccountID(ctx, constant.AccountTypeAccruedInterestPayable, "", product.Currency)
	if err != nil {
		return SavingsProduct{}, fmt.Errorf("%w: accrued interest payable account is unavailable: %v", apperror.ErrValidation, err)
	}
	if product.InterestExpenseAccountID != expectedExpense || product.InterestPayableAccountID != expectedPayable {
		return SavingsProduct{}, fmt.Errorf("%w: savings system accounts must match the product currency", apperror.ErrValidation)
	}
	if product.Timezone != "" {
		if _, err := time.LoadLocation(product.Timezone); err != nil {
			return SavingsProduct{}, fmt.Errorf("%w: invalid savings product timezone", apperror.ErrValidation)
		}
	}
	return m.c5InterestRepo.CreateProduct(ctx, product)
}

func (m *Module) GetSavingsProduct(ctx context.Context, id uuid.UUID) (SavingsProduct, error) {
	return m.c5InterestRepo.GetProduct(ctx, id)
}

func (m *Module) ListSavingsProducts(ctx context.Context, status string) ([]SavingsProduct, error) {
	return m.c5InterestRepo.ListProducts(ctx, status)
}

func (m *Module) SetSavingsProductStatus(ctx context.Context, id uuid.UUID, status, checker string) (SavingsProduct, error) {
	if checker == "" {
		return SavingsProduct{}, fmt.Errorf("%w: product checker is required", apperror.ErrValidation)
	}
	product, err := m.c5InterestRepo.GetProduct(ctx, id)
	if err != nil {
		return SavingsProduct{}, err
	}
	if product.CreatedBy == checker {
		return SavingsProduct{}, fmt.Errorf("%w: product maker and checker must differ", apperror.ErrValidation)
	}
	validTransition := (status == model.SavingsProductActive && (product.Status == model.SavingsProductDraft || product.Status == model.SavingsProductIntakePaused)) ||
		(status == model.SavingsProductIntakePaused && product.Status == model.SavingsProductActive) ||
		(status == model.SavingsProductRetired && (product.Status == model.SavingsProductActive || product.Status == model.SavingsProductIntakePaused))
	if !validTransition {
		return SavingsProduct{}, fmt.Errorf("%w: invalid savings product status transition", apperror.ErrValidation)
	}
	if err := m.c5InterestRepo.UpdateProductStatus(ctx, id, status, checker); err != nil {
		return SavingsProduct{}, err
	}
	return m.c5InterestRepo.GetProduct(ctx, id)
}

func (m *Module) CreateSavingsRate(ctx context.Context, rate SavingsRateVersion) (SavingsRateVersion, error) {
	if rate.CreatedBy == "" || rate.ProductID == uuid.Nil || rate.AnnualRateBps < 0 || rate.AnnualRateBps > 2000 {
		return SavingsRateVersion{}, fmt.Errorf("%w: invalid savings rate", apperror.ErrValidation)
	}
	if rate.Status != "" && rate.Status != "draft" {
		return SavingsRateVersion{}, fmt.Errorf("%w: savings rate must be created in draft status", apperror.ErrValidation)
	}
	rate.Status = "draft"
	if rate.EffectiveFrom.IsZero() || (rate.EffectiveUntil != nil && !rate.EffectiveUntil.After(rate.EffectiveFrom)) {
		return SavingsRateVersion{}, fmt.Errorf("%w: invalid savings rate effective window", apperror.ErrValidation)
	}
	if len(rate.ContentHash) == 0 {
		until := ""
		if rate.EffectiveUntil != nil {
			until = rate.EffectiveUntil.Format("2006-01-02")
		}
		terms, _ := json.Marshal(struct {
			ProductID       string `json:"product_id"`
			AnnualRateBPS   int    `json:"annual_rate_bps"`
			EffectiveFrom   string `json:"effective_from"`
			EffectiveUntil   string `json:"effective_until,omitempty"`
		}{
			ProductID:     rate.ProductID.String(),
			AnnualRateBPS: rate.AnnualRateBps,
			EffectiveFrom: rate.EffectiveFrom.Format("2006-01-02"),
			EffectiveUntil: until,
		})
		digest := sha256.Sum256(terms)
		rate.ContentHash = digest[:]
	}
	return m.c5InterestRepo.CreateRate(ctx, rate)
}

func (m *Module) SubmitSavingsRate(ctx context.Context, id uuid.UUID, maker string) error {
	if maker == "" {
		return fmt.Errorf("%w: rate maker is required", apperror.ErrValidation)
	}
	return m.c5InterestRepo.SubmitRate(ctx, id, maker)
}

func (m *Module) ApproveSavingsRate(ctx context.Context, id uuid.UUID, checker string) error {
	if checker == "" {
		return fmt.Errorf("%w: rate checker is required", apperror.ErrValidation)
	}
	return m.c5InterestRepo.ApproveRate(ctx, id, checker)
}

func (m *Module) RejectSavingsRate(ctx context.Context, id uuid.UUID, checker, reason string) error {
	if checker == "" {
		return fmt.Errorf("%w: rate checker is required", apperror.ErrValidation)
	}
	return m.c5InterestRepo.RejectRate(ctx, id, checker, reason)
}

func (m *Module) EnrollSavingsAccount(ctx context.Context, enrollment SavingsEnrollment) (SavingsEnrollment, error) {
	if enrollment.ProductID == uuid.Nil || enrollment.AccountID == uuid.Nil || enrollment.UserID == uuid.Nil || enrollment.CreatedBy == "" {
		return SavingsEnrollment{}, fmt.Errorf("%w: product, account, user, and created_by are required", apperror.ErrValidation)
	}
	product, err := m.c5InterestRepo.GetProduct(ctx, enrollment.ProductID)
	if err != nil {
		return SavingsEnrollment{}, err
	}
	if product.Status != model.SavingsProductActive {
		return SavingsEnrollment{}, fmt.Errorf("%w: savings product is not active", apperror.ErrValidation)
	}
	if enrollment.EffectiveFrom.IsZero() {
		return SavingsEnrollment{}, fmt.Errorf("%w: enrollment effective_from is required", apperror.ErrValidation)
	}
	if enrollment.EffectiveUntil != nil && !enrollment.EffectiveUntil.After(enrollment.EffectiveFrom) {
		return SavingsEnrollment{}, fmt.Errorf("%w: invalid enrollment effective window", apperror.ErrValidation)
	}
	var accountType, accountCurrency string
	if err := m.db.QueryRowContext(ctx, `SELECT type, currency FROM accounts
		WHERE id=$1 AND owner_type='user' AND owner_id=$2 AND status='active'`, enrollment.AccountID, enrollment.UserID).
		Scan(&accountType, &accountCurrency); err != nil {
		return SavingsEnrollment{}, fmt.Errorf("%w: savings account is unavailable or not owned by user", apperror.ErrValidation)
	}
	if accountCurrency != product.Currency || !slices.Contains(product.EligibleAccountTypes, accountType) {
		return SavingsEnrollment{}, fmt.Errorf("%w: savings account is not eligible for product", apperror.ErrValidation)
	}
	if enrollment.Status == "" {
		enrollment.Status = model.SavingsEnrollmentActive
	}
	if enrollment.Status != model.SavingsEnrollmentPending && enrollment.Status != model.SavingsEnrollmentActive {
		return SavingsEnrollment{}, fmt.Errorf("%w: enrollment must be created as pending or active", apperror.ErrValidation)
	}
	if enrollment.Mode == "" {
		enrollment.Mode = "monthly_liability_capitalization"
	}
	if enrollment.Mode != "monthly_liability_capitalization" {
		return SavingsEnrollment{}, fmt.Errorf("%w: C5 enrollment mode must be monthly_liability_capitalization", apperror.ErrValidation)
	}
	if enrollment.UpdatedBy == "" {
		enrollment.UpdatedBy = enrollment.CreatedBy
	}
	return m.c5InterestRepo.CreateEnrollment(ctx, enrollment)
}

func (m *Module) GetSavingsEnrollment(ctx context.Context, id uuid.UUID) (SavingsEnrollment, error) {
	return m.c5InterestRepo.GetEnrollment(ctx, id)
}

func (m *Module) ListSavingsEnrollments(ctx context.Context, userID uuid.UUID) ([]SavingsEnrollment, error) {
	return m.c5InterestRepo.ListEnrollments(ctx, userID)
}

func (m *Module) ListInterestAccruals(ctx context.Context, enrollmentID uuid.UUID) ([]InterestDailyAccrual, error) {
	return m.c5InterestRepo.ListEnrollmentAccruals(ctx, enrollmentID)
}

func (m *Module) ListInterestPeriods(ctx context.Context, enrollmentID uuid.UUID) ([]InterestPeriod, error) {
	return m.c5InterestRepo.ListEnrollmentPeriods(ctx, enrollmentID)
}

func (m *Module) ListInterestCapitalizations(ctx context.Context, enrollmentID uuid.UUID) ([]InterestCapitalizationItem, error) {
	return m.c5InterestRepo.ListEnrollmentCapitalizations(ctx, enrollmentID)
}

func (m *Module) RunInterestDaily(ctx context.Context, date time.Time) interestservice.DailyRunSummary {
	if snapshotter, ok := m.snapshotRepo.(interface {
		InsertForDateAllAccounts(context.Context, time.Time) (int, error)
	}); ok {
		if _, err := snapshotter.InsertForDateAllAccounts(ctx, date); err != nil {
			m.logger.Error("c5: complete closing snapshot failed", slog.Any("error", err), slog.String("date", date.Format("2006-01-02")))
		}
	}
	return m.interestSvc.RunDaily(ctx, date)
}

func (m *Module) RunInterestPeriodCloseDue(ctx context.Context, now time.Time, actor string) (closed, failed int, err error) {
	return m.interestSvc.CloseDuePeriods(ctx, now, actor)
}

func (m *Module) GetInterestPeriod(ctx context.Context, id uuid.UUID) (InterestPeriod, error) {
	return m.c5InterestRepo.GetPeriod(ctx, id)
}

func (m *Module) PreviewInterestPeriodClose(ctx context.Context, id uuid.UUID) (interestservice.PeriodPreview, error) {
	return m.interestSvc.PreviewPeriodClose(ctx, id)
}

func (m *Module) RunInterestPeriodClose(ctx context.Context, id uuid.UUID, actor string) error {
	return m.interestSvc.ClosePeriod(ctx, id, actor)
}

func (m *Module) updateSavingsEnrollmentStatus(ctx context.Context, id uuid.UUID, status, checker string, effectiveUntil *time.Time) error {
	if checker == "" {
		return fmt.Errorf("%w: enrollment checker is required", apperror.ErrValidation)
	}
	enrollment, err := m.c5InterestRepo.GetEnrollment(ctx, id)
	if err != nil {
		return err
	}
	if enrollment.CreatedBy == checker {
		return fmt.Errorf("%w: enrollment maker and checker must differ", apperror.ErrValidation)
	}
	validTransition := (status == model.SavingsEnrollmentAccrualPaused && enrollment.Status == model.SavingsEnrollmentActive) ||
		(status == model.SavingsEnrollmentActive && enrollment.Status == model.SavingsEnrollmentAccrualPaused) ||
		(status == model.SavingsEnrollmentEnded && (enrollment.Status == model.SavingsEnrollmentActive || enrollment.Status == model.SavingsEnrollmentAccrualPaused))
	if !validTransition {
		return fmt.Errorf("%w: invalid savings enrollment status transition", apperror.ErrValidation)
	}
	if status == model.SavingsEnrollmentEnded {
		if effectiveUntil == nil || !effectiveUntil.After(enrollment.EffectiveFrom) {
			return fmt.Errorf("%w: enrollment end must be after effective_from", apperror.ErrValidation)
		}
	}
	product, err := m.c5InterestRepo.GetProduct(ctx, enrollment.ProductID)
	if err != nil {
		return err
	}
	loc, err := time.LoadLocation(product.Timezone)
	if err != nil {
		return fmt.Errorf("%w: invalid savings product timezone", apperror.ErrValidation)
	}
	now := time.Now().In(loc)
	effectiveFrom := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, loc)
	if status == model.SavingsEnrollmentEnded && effectiveUntil != nil {
		effectiveFrom = *effectiveUntil
	}
	if updater, ok := m.c5InterestRepo.(interface {
		UpdateEnrollmentStatusWithEffectiveDate(context.Context, uuid.UUID, string, string, *time.Time, *time.Time) error
	}); ok {
		return updater.UpdateEnrollmentStatusWithEffectiveDate(ctx, id, status, checker, &effectiveFrom, effectiveUntil)
	}
	updater, ok := m.c5InterestRepo.(interface {
		UpdateEnrollmentStatus(context.Context, uuid.UUID, string, string, *time.Time) error
	})
	if !ok {
		return fmt.Errorf("%w: savings enrollment lifecycle is unavailable", apperror.ErrValidation)
	}
	return updater.UpdateEnrollmentStatus(ctx, id, status, checker, effectiveUntil)
}

func (m *Module) PauseSavingsEnrollment(ctx context.Context, id uuid.UUID, checker string) error {
	return m.updateSavingsEnrollmentStatus(ctx, id, model.SavingsEnrollmentAccrualPaused, checker, nil)
}

func (m *Module) ResumeSavingsEnrollment(ctx context.Context, id uuid.UUID, checker string) error {
	return m.updateSavingsEnrollmentStatus(ctx, id, model.SavingsEnrollmentActive, checker, nil)
}

func (m *Module) EndSavingsEnrollment(ctx context.Context, id uuid.UUID, checker string) error {
	enrollment, err := m.c5InterestRepo.GetEnrollment(ctx, id)
	if err != nil {
		return err
	}
	product, err := m.c5InterestRepo.GetProduct(ctx, enrollment.ProductID)
	if err != nil {
		return err
	}
	loc, err := time.LoadLocation(product.Timezone)
	if err != nil {
		return fmt.Errorf("%w: invalid savings product timezone", apperror.ErrValidation)
	}
	now := time.Now().In(loc)
	effectiveUntil := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, loc)
	return m.updateSavingsEnrollmentStatus(ctx, id, model.SavingsEnrollmentEnded, checker, &effectiveUntil)
}

func (m *Module) RetryInterestPeriodItem(ctx context.Context, id uuid.UUID) error {
	return m.interestSvc.RetryPeriodItem(ctx, id)
}

func (m *Module) CreateInterestAdjustment(ctx context.Context, adjustment InterestAdjustment) (InterestAdjustment, error) {
	if adjustment.CreatedBy == "" || adjustment.Reason == "" || adjustment.Amount <= 0 {
		return InterestAdjustment{}, fmt.Errorf("%w: adjustment reason, amount, and creator are required", apperror.ErrValidation)
	}
	return m.interestSvc.CreateAdjustment(ctx, adjustment)
}

func (m *Module) ApproveInterestAdjustment(ctx context.Context, id uuid.UUID, checker string) error {
	return m.interestSvc.ApproveAdjustment(ctx, id, checker)
}

// GetDailyPositionReport/GetDailyMutationReport/GetReconSummaryReport read
// the three regulatory-reporting views (docs/roadmap/archive/20 Task T2,
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
