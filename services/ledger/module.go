// Package ledger is the stable public facade for Seev's double-entry ledger.
// Composition and use-case orchestration live in internal/ledger; models,
// repositories, processors, transport, and workers are kept in dedicated
// packages beneath the module.
package ledger

import (
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/shopspring/decimal"

	"github.com/herdifirdausss/seev/contracts/clients/fraud"
	"github.com/herdifirdausss/seev/internal/platform/database"
	"github.com/herdifirdausss/seev/internal/platform/messaging"
	"github.com/herdifirdausss/seev/internal/platform/security/crypto"
	ledgerinternal "github.com/herdifirdausss/seev/services/ledger/internal/ledger"
)

var (
	ErrAlreadyClosed       = ledgerinternal.ErrAlreadyClosed
	ErrQuoteExpired        = ledgerinternal.ErrQuoteExpired
	ErrQuoteMismatch       = ledgerinternal.ErrQuoteMismatch
	ErrTransactionNotFound = ledgerinternal.ErrTransactionNotFound
	ErrAccountNotFound     = ledgerinternal.ErrAccountNotFound
)

type (
	Account                       = ledgerinternal.Account
	Balance                       = ledgerinternal.Balance
	ChargebackDispute             = ledgerinternal.ChargebackDispute
	ChargebackDisputeStatusChange = ledgerinternal.ChargebackDisputeStatusChange
	Command                       = ledgerinternal.Command
	CurrencyBalance               = ledgerinternal.CurrencyBalance
	CurrencyInfo                  = ledgerinternal.CurrencyInfo
	DeadOutboxEvent               = ledgerinternal.DeadOutboxEvent
	DisbursementBatchReport       = ledgerinternal.DisbursementBatchReport
	DisbursementImportRow         = ledgerinternal.DisbursementImportRow
	DisbursementRunResult         = ledgerinternal.DisbursementRunResult
	Entry                         = ledgerinternal.Entry
	FXConversion                  = ledgerinternal.FXConversion
	FXConversionReconciliation    = ledgerinternal.FXConversionReconciliation
	FXDailyPosition               = ledgerinternal.FXDailyPosition
	FXDirection                   = ledgerinternal.FXDirection
	FXPair                        = ledgerinternal.FXPair
	FXPosition                    = ledgerinternal.FXPosition
	FXQuote                       = ledgerinternal.FXQuote
	FXRateVersion                 = ledgerinternal.FXRateVersion
	FXReconciliationReport        = ledgerinternal.FXReconciliationReport
	InterestAdjustment            = ledgerinternal.InterestAdjustment
	InterestCapitalizationItem    = ledgerinternal.InterestCapitalizationItem
	InterestDailyAccrual          = ledgerinternal.InterestDailyAccrual
	InterestPeriod                = ledgerinternal.InterestPeriod
	InterestPeriodPreview         = ledgerinternal.InterestPeriodPreview
	LedgerError                   = ledgerinternal.LedgerError
	Module                        = ledgerinternal.Module
	PendingAdjustment             = ledgerinternal.PendingAdjustment
	PolicyChecker                 = ledgerinternal.PolicyChecker
	Quote                         = ledgerinternal.Quote
	ReconBatch                    = ledgerinternal.ReconBatch
	ReconBatchReport              = ledgerinternal.ReconBatchReport
	ReconImportRow                = ledgerinternal.ReconImportRow
	ReportDailyMutation           = ledgerinternal.ReportDailyMutation
	ReportDailyPosition           = ledgerinternal.ReportDailyPosition
	ReportReconSummary            = ledgerinternal.ReportReconSummary
	SavingsConfig                 = ledgerinternal.SavingsConfig
	SavingsEnrollment             = ledgerinternal.SavingsEnrollment
	SavingsProduct                = ledgerinternal.SavingsProduct
	SavingsRateVersion            = ledgerinternal.SavingsRateVersion
	ScheduledExecutionAttempt     = ledgerinternal.ScheduledExecutionAttempt
	ScheduledOccurrence           = ledgerinternal.ScheduledOccurrence
	ScheduledPolicy               = ledgerinternal.ScheduledPolicy
	ScheduledTransaction          = ledgerinternal.ScheduledTransaction
	Statement                     = ledgerinternal.Statement
	Transaction                   = ledgerinternal.Transaction
	WorkerConfig                  = ledgerinternal.WorkerConfig
)

func NewModule(db database.DatabaseSQL, broker messaging.Broker, redisClient *redis.Client, workerCfg WorkerConfig, logger *slog.Logger, maxAmountPerTx decimal.Decimal, policyChecker PolicyChecker, fraudClient *fraudcheck.Client, feeQuoteTTL time.Duration, digestRing *cryptox.DigestRing, cryptoxRing *cryptox.Ring) *Module {
	return ledgerinternal.NewModule(db, broker, redisClient, workerCfg, logger, maxAmountPerTx, policyChecker, fraudClient, feeQuoteTTL, digestRing, cryptoxRing)
}
