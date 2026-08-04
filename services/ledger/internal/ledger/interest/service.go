package interest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/herdifirdausss/seev/contracts/events/ledger"
	"github.com/herdifirdausss/seev/internal/platform/database/identifiers"
	"github.com/herdifirdausss/seev/services/ledger/internal/ledger/errors"
	"github.com/herdifirdausss/seev/services/ledger/internal/ledger/model"
	"github.com/herdifirdausss/seev/services/ledger/internal/processors"
	"github.com/herdifirdausss/seev/services/ledger/internal/repository"
	commandservice "github.com/herdifirdausss/seev/services/ledger/internal/ledger/command"
)

var (
	ErrSnapshotMissing        = errors.New("interest snapshot missing")
	ErrSavingsRateMissing     = errors.New("savings rate missing")
	ErrPriorAccrualIncomplete = errors.New("prior interest accrual is incomplete")
	ErrPeriodNotReady         = errors.New("interest period not ready")
	ErrClosedPeriodImmutable  = errors.New("closed interest period is immutable")
)

type DatabaseSQL interface {
	WithTx(context.Context, *sql.TxOptions, func(*sql.Tx) error) error
}

type Poster interface {
	Handle(context.Context, processors.Command) error
}

type BalanceReader interface {
	BalanceAsOf(context.Context, uuid.UUID, time.Time) (decimal.Decimal, error)
}

type SnapshotReader interface {
	SnapshotAt(context.Context, uuid.UUID, time.Time) (uuid.UUID, decimal.Decimal, bool, error)
}

type TransactionLookup interface {
	GetTransactionByIdempotencyKey(context.Context, string, string) (model.LedgerTransaction, error)
}

type Service struct {
	db       DatabaseSQL
	repo     repository.InterestRepository
	snapshot BalanceReader
	poster   Poster
	txLookup TransactionLookup
	outbox   repository.OutboxRepository
	logger   *slog.Logger
	loc      *time.Location
}

func New(db DatabaseSQL, repo repository.InterestRepository, snapshot BalanceReader, poster Poster, logger *slog.Logger, loc *time.Location) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	if loc == nil {
		loc = time.FixedZone("Asia/Jakarta", 7*60*60)
	}
	return &Service{db: db, repo: repo, snapshot: snapshot, poster: poster, logger: logger, loc: loc}
}

func (s *Service) SetTransactionLookup(lookup TransactionLookup) { s.txLookup = lookup }

// SetOutbox wires the Ledger-owned transactional outbox after construction.
// It is a setter to keep existing C5 service/test constructors source
// compatible while the production module still makes domain-event delivery
// mandatory by wiring its real repository here.
func (s *Service) SetOutbox(outbox repository.OutboxRepository) { s.outbox = outbox }

// RetryPeriodItem resets a blocked/failed durable period item without making
// the broad repository contract mandatory for existing test doubles.  The
// item id space is shared by daily accrual and capitalization rows, so the
// concrete repository is probed in that order.
func (s *Service) RetryPeriodItem(ctx context.Context, id uuid.UUID) error {
	if retryer, ok := s.repo.(interface {
		RetryDailyAccrual(context.Context, uuid.UUID) error
	}); ok {
		err := retryer.RetryDailyAccrual(ctx, id)
		if err == nil {
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	if retryer, ok := s.repo.(interface {
		RetryCapitalization(context.Context, uuid.UUID) error
	}); ok {
		err := retryer.RetryCapitalization(ctx, id)
		if err == nil {
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	return fmt.Errorf("%w: interest period item %s is not retryable or does not exist", apperror.ErrValidation, id)
}

type DailyRunSummary struct {
	Date          time.Time
	Completed     int
	CompletedZero int
	RetryWait     int
	Blocked       int
	Failed        int
}

// RunDaily creates a durable row for every active enrollment and processes the
// rows independently.  One missing snapshot/rate or one Ledger failure never
// prevents another account from receiving its own durable outcome.
func (s *Service) RunDaily(ctx context.Context, date time.Time) DailyRunSummary {
	date = date.In(s.loc)
	summary := DailyRunSummary{Date: date}
	enrollments, err := s.repo.ListActiveEnrollments(ctx, date)
	if err != nil {
		s.logger.Error("interest: list active enrollments failed", "error", err)
		s.recoverPending(ctx)
		return summary
	}
	for _, enrollment := range enrollments {
		if err := s.accrueOne(ctx, enrollment, date); err != nil {
			s.logger.Error("interest: daily accrual failed", "enrollment_id", enrollment.ID.String(), "error", err)
		}
		// Re-read status only for reporting if the repository is available;
		// the durable row is the source of truth, not this in-memory count.
	}
	s.recoverPending(ctx)
	return summary
}

func (s *Service) recoverPending(ctx context.Context) {
	for {
		item, err := s.repo.ClaimDailyAccrual(ctx, "interest-recovery-"+uuid.NewString(), time.Now().UTC())
		if errors.Is(err, sql.ErrNoRows) {
			return
		}
		if err != nil {
			s.logger.Error("interest: claim retryable accrual failed", "error", err)
			return
		}
		enrollment, err := s.repo.GetEnrollment(ctx, item.EnrollmentID)
		if err != nil {
			_ = s.repo.FailDailyAccrual(ctx, item.ID, model.InterestAccrualRetryWait, "INTEREST_ENROLLMENT_READ_FAILED", time.Now().UTC().Add(5*time.Minute))
			s.logger.Error("interest: load retryable enrollment failed", "accrual_id", item.ID.String(), "error", err)
			continue
		}
		product, err := s.repo.GetProduct(ctx, enrollment.ProductID)
		if err != nil {
			_ = s.repo.FailDailyAccrual(ctx, item.ID, model.InterestAccrualRetryWait, "INTEREST_PRODUCT_READ_FAILED", time.Now().UTC().Add(5*time.Minute))
			s.logger.Error("interest: load retryable product failed", "accrual_id", item.ID.String(), "error", err)
			continue
		}
		period, err := s.repo.GetPeriod(ctx, item.PeriodID)
		if err != nil {
			_ = s.repo.FailDailyAccrual(ctx, item.ID, model.InterestAccrualRetryWait, "INTEREST_PERIOD_READ_FAILED", time.Now().UTC().Add(5*time.Minute))
			s.logger.Error("interest: load retryable period failed", "accrual_id", item.ID.String(), "error", err)
			continue
		}
		if err := s.processClaimedAccrual(ctx, enrollment, product, period, item, item.AccrualDate.In(s.loc)); err != nil {
			s.logger.Error("interest: retryable accrual failed", "accrual_id", item.ID.String(), "error", err)
		}
	}
}

func (s *Service) accrueOne(ctx context.Context, enrollment model.SavingsEnrollment, date time.Time) error {
	product, err := s.repo.GetProduct(ctx, enrollment.ProductID)
	if err != nil {
		return err
	}
	period, err := s.ensurePeriodForDate(ctx, product, date)
	if err != nil {
		return err
	}
	item, err := s.repo.CreateOrGetDailyAccrual(ctx, model.InterestDailyAccrual{
		PeriodID: period.ID, EnrollmentID: enrollment.ID, AccountID: enrollment.AccountID,
		AccrualDate: date,
	})
	if err != nil {
		return err
	}
	if isTerminalAccrual(item.Status) {
		return nil
	}
	owner := "interest-" + uuid.NewString()
	item, err = s.repo.ClaimDailyAccrualForID(ctx, item.ID, owner, time.Now().UTC())
	if err != nil {
		return err
	}
	if item.Status != model.InterestAccrualProcessing {
		return nil
	}
	return s.processClaimedAccrual(ctx, enrollment, product, period, item, date)
}

func (s *Service) processClaimedAccrual(ctx context.Context, enrollment model.SavingsEnrollment, product model.SavingsProduct, period model.InterestPeriod, item model.InterestDailyAccrual, date time.Time) error {
	snapshotID, balance, found, err := s.readSnapshot(ctx, enrollment.AccountID, date)
	if err != nil {
		return s.blockOrRetry(ctx, period, item.ID, "INTEREST_SNAPSHOT_READ_FAILED", err)
	}
	if !found {
		return s.blockOrRetry(ctx, period, item.ID, "INTEREST_SNAPSHOT_MISSING", ErrSnapshotMissing)
	}
	rate, err := s.repo.GetRateForDate(ctx, enrollment.ProductID, date)
	if err != nil {
		return s.blockOrRetry(ctx, period, item.ID, "SAVINGS_RATE_MISSING", fmt.Errorf("%w: %v", ErrSavingsRateMissing, err))
	}
	balanceInt, err := balanceInt64(balance)
	if err != nil {
		return s.blockOrRetry(ctx, period, item.ID, "INTEREST_BALANCE_NOT_INTEGER", err)
	}
	carryNumerator := enrollment.CarryNumerator
	carryDenominator := enrollment.CarryDenominator
	if priorCarry, priorDenominator, found, carryErr := s.repo.GetCarryBeforeDate(ctx, enrollment.ID, date); carryErr != nil {
		return s.blockOrRetry(ctx, period, item.ID, "INTEREST_PRIOR_ACCRUAL_INCOMPLETE", fmt.Errorf("%w: %v", ErrPriorAccrualIncomplete, carryErr))
	} else if found {
		carryNumerator = priorCarry
		carryDenominator = priorDenominator
	}
	if carryDenominator == "" {
		carryDenominator = BigIntString(big.NewInt(DailyDenominator))
	}
	if carryDenominator != BigIntString(big.NewInt(DailyDenominator)) {
		return s.blockOrRetry(ctx, period, item.ID, "INTEREST_CARRY_DENOMINATOR_INVALID", fmt.Errorf("carry denominator must be %d", DailyDenominator))
	}
	eligibleBalance := balanceInt
	if eligibleBalance <= 0 || (product.MinimumEligibleBalance > 0 && eligibleBalance < product.MinimumEligibleBalance) {
		eligibleBalance = 0
	}
	calculation, err := CalculateDailyFromStrings(eligibleBalance, int64(rate.AnnualRateBps), carryNumerator)
	if err != nil {
		return s.blockOrRetry(ctx, period, item.ID, "INTEREST_CALCULATION_INVALID", err)
	}
	recognized, err := bigIntInt64(calculation.RecognizedMinor)
	if err != nil {
		return s.blockOrRetry(ctx, period, item.ID, "INTEREST_AMOUNT_OVERFLOW", err)
	}
	item.SnapshotID = uuidPtrOrNil(snapshotID)
	item.ClosingBalance = &balanceInt
	item.RateVersionID = &rate.ID
	item.AnnualRateBps = &rate.AnnualRateBps
	item.ExactNumerator = BigIntString(calculation.ExactNumerator)
	item.Denominator = BigIntString(calculation.Denominator)
	item.OpeningCarryNumerator = BigIntString(calculation.OpeningCarryNumerator)
	item.RecognizedAmount = &recognized
	item.ClosingCarryNumerator = BigIntString(calculation.ClosingCarryNumerator)
	if err := s.db.WithTx(ctx, nil, func(tx *sql.Tx) error { return s.repo.SaveDailyCalculation(ctx, tx, item) }); err != nil {
		return err
	}

	if recognized == 0 {
		return s.completeDailyAccrual(ctx, product, item, model.InterestAccrualCompletedZero, nil)
	}

	key := "interest-liability:" + enrollment.ID.String() + ":" + date.Format("2006-01-02")
	postErr := commandservice.Run(ctx, s.poster, processors.Command{
		IdempotencyKey: key, IdempotencyScope: enrollment.ID.String(),
		Type: "interest_liability_accrue", Amount: decimal.NewFromInt(recognized),
		Metadata: map[string]any{
			"account_id": enrollment.AccountID.String(), "enrollment_id": enrollment.ID.String(),
			"period_id": period.ID.String(), "accrual_date": date.Format("2006-01-02"),
			"rate_bps": fmt.Sprintf("%d", rate.AnnualRateBps),
		},
	}, commandservice.ExecutionContext{Source: "internal-worker", CorrelationID: key, RequestOrigin: "interest-period-accrual"})
	var txID *uuid.UUID
	if postErr != nil && !errors.Is(postErr, apperror.ErrAlreadyPosted) {
		return s.blockOrRetry(ctx, period, item.ID, classifyPostError(postErr), postErr)
	}
	if s.txLookup != nil {
		tx, lookupErr := s.txLookup.GetTransactionByIdempotencyKey(ctx, key, enrollment.ID.String())
		if lookupErr != nil {
			// The ledger post may already have committed. Do not complete the
			// durable accrual until the audit link can be resolved; the lease
			// recovery path will retry with the same idempotency key.
			return s.blockOrRetry(ctx, period, item.ID, "INTEREST_TRANSACTION_LOOKUP_FAILED", lookupErr)
		}
		txID = &tx.ID
	}
	return s.completeDailyAccrual(ctx, product, item, model.InterestAccrualCompletedPosted, txID)
}

func (s *Service) completeDailyAccrual(ctx context.Context, product model.SavingsProduct, item model.InterestDailyAccrual, status string, transactionID *uuid.UUID) error {
	return s.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		if err := s.repo.CompleteDailyAccrual(ctx, tx, item.ID, status, transactionID,
			item.ClosingCarryNumerator, item.Denominator); err != nil {
			return err
		}
		amount := int64(0)
		if item.RecognizedAmount != nil {
			amount = *item.RecognizedAmount
		}
		return s.insertOutbox(ctx, tx, model.OutboxEvent{
			AggregateType: "interest_accrual",
			AggregateID:   item.ID,
			EventType:     events.TypeInterestAccrued,
			Payload: events.NewInterestAccrued(item.ID, item.PeriodID, item.EnrollmentID,
				item.AccountID, item.AccrualDate.Format("2006-01-02"), fmt.Sprintf("%d", amount),
				product.Currency, transactionID, time.Now().UTC()).ToPayload(),
		})
	})
}

func (s *Service) insertOutbox(ctx context.Context, tx *sql.Tx, event model.OutboxEvent) error {
	if s.outbox == nil {
		return nil
	}
	return s.outbox.InsertEvents(ctx, tx, []model.OutboxEvent{event})
}

func (s *Service) readSnapshot(ctx context.Context, accountID uuid.UUID, date time.Time) (uuid.UUID, decimal.Decimal, bool, error) {
	if reader, ok := s.snapshot.(SnapshotReader); ok {
		return reader.SnapshotAt(ctx, accountID, date)
	}
	balance, err := s.snapshot.BalanceAsOf(ctx, accountID, date)
	return uuid.Nil, balance, err == nil, err
}

func balanceInt64(balance decimal.Decimal) (int64, error) {
	if !balance.Equal(balance.Truncate(0)) {
		return 0, errors.New("interest: snapshot balance is not an integer minor-unit value")
	}
	if !balance.IsInteger() {
		return 0, errors.New("interest: snapshot balance is not integral")
	}
	if !balance.BigInt().IsInt64() {
		return 0, errors.New("interest: snapshot balance exceeds int64")
	}
	return balance.IntPart(), nil
}

func bigIntInt64(value *big.Int) (int64, error) {
	if !value.IsInt64() {
		return 0, errors.New("interest: recognized amount exceeds int64")
	}
	return value.Int64(), nil
}

func uuidPtrOrNil(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}

func (s *Service) blockOrRetry(ctx context.Context, period model.InterestPeriod, id uuid.UUID, code string, cause error) error {
	status := model.InterestAccrualRetryWait
	next := time.Now().UTC().Add(5 * time.Minute)
	hardBlock := code == "INTEREST_BALANCE_NOT_INTEGER" || code == "INTEREST_CALCULATION_INVALID" || code == "INTEREST_AMOUNT_OVERFLOW" || code == "INTEREST_CARRY_DENOMINATOR_INVALID" || code == "INTEREST_LIABILITY_POST_BLOCKED"
	if hardBlock || (!period.AccrualCutoffAt.IsZero() && !time.Now().UTC().Before(period.AccrualCutoffAt.UTC())) {
		status = model.InterestAccrualBlocked
		next = time.Time{}
	}
	if err := s.repo.FailDailyAccrual(ctx, id, status, code, next); err != nil {
		return fmt.Errorf("%s: %w; record failure: %v", code, cause, err)
	}
	return fmt.Errorf("%s: %w", code, cause)
}

func classifyPostError(err error) string {
	if isBusinessFailure(err) {
		return "INTEREST_LIABILITY_POST_BLOCKED"
	}
	return "INTEREST_LIABILITY_POST_RETRY"
}

func isBusinessFailure(err error) bool {
	var business *apperror.LedgerError
	return errors.As(err, &business)
}

func isTerminalAccrual(status string) bool {
	return status == model.InterestAccrualCompletedZero || status == model.InterestAccrualCompletedPosted || status == model.InterestAccrualAdjusted
}

func (s *Service) ensurePeriodForDate(ctx context.Context, product model.SavingsProduct, date time.Time) (model.InterestPeriod, error) {
	loc, err := time.LoadLocation(product.Timezone)
	if err != nil {
		loc = s.loc
	}
	local := date.In(loc)
	start := time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, loc)
	end := start.AddDate(0, 1, 0)
	closeAt := end.Add(75 * time.Minute)
	return s.repo.EnsurePeriod(ctx, model.InterestPeriod{
		ProductID: product.ID, Currency: product.Currency, PeriodYear: start.Year(),
		PeriodMonth: int(start.Month()), PeriodStartAt: start.UTC(), PeriodEndAt: end.UTC(),
		AccrualCutoffAt: end.UTC(), CloseNotBeforeAt: closeAt.UTC(), Status: model.InterestPeriodOpen,
	})
}

type PeriodPreview struct {
	PeriodID               uuid.UUID `json:"period_id"`
	EligibleEnrollments    int       `json:"eligible_enrollments"`
	ExpectedItems          int64     `json:"expected_items"`
	DailyAccruals          int       `json:"daily_accruals"`
	ZeroAccruals           int       `json:"zero_accruals"`
	PostedLiability        int64     `json:"posted_liability_amount"`
	BlockedItems           int       `json:"blocked_items"`
	MissingItems           int       `json:"missing_items"`
	ExpectedCapitalization int64     `json:"expected_capitalization_amount"`
	SnapshotComplete       bool      `json:"snapshot_complete"`
	CarryChainContinuous   bool      `json:"carry_chain_continuous"`
	RateCoverageComplete   bool      `json:"rate_coverage_complete"`
	AccountCloseBlocked    bool      `json:"account_close_blocked"`
	Ready                  bool      `json:"ready"`
	Reason                 string    `json:"reason,omitempty"`
}

func (s *Service) PreviewPeriodClose(ctx context.Context, periodID uuid.UUID) (PeriodPreview, error) {
	period, err := s.repo.GetPeriod(ctx, periodID)
	if err != nil {
		return PeriodPreview{}, err
	}
	if period.Status != model.InterestPeriodClosed {
		if err := s.repo.RefreshExpectedItemCount(ctx, periodID); err != nil {
			return PeriodPreview{}, err
		}
		period, err = s.repo.GetPeriod(ctx, periodID)
		if err != nil {
			return PeriodPreview{}, err
		}
	}
	accruals, err := s.repo.ListPeriodAccruals(ctx, periodID)
	if err != nil {
		return PeriodPreview{}, err
	}
	eligibleEnrollments, err := s.repo.CountEligibleEnrollments(ctx, periodID)
	if err != nil {
		return PeriodPreview{}, err
	}
	preview := PeriodPreview{PeriodID: period.ID, EligibleEnrollments: eligibleEnrollments, ExpectedItems: period.ExpectedItemCount, DailyAccruals: len(accruals), SnapshotComplete: true, CarryChainContinuous: true, RateCoverageComplete: true}
	accountCloseBlocked, err := s.repo.HasNonActiveCapitalizationAccount(ctx, periodID)
	if err != nil {
		return PeriodPreview{}, err
	}
	preview.AccountCloseBlocked = accountCloseBlocked
	lastCarry := make(map[uuid.UUID]string)
	for _, item := range accruals {
		terminal := item.Status == model.InterestAccrualCompletedZero || item.Status == model.InterestAccrualCompletedPosted || item.Status == model.InterestAccrualAdjusted
		if terminal {
			if item.SnapshotID == nil || item.ClosingBalance == nil {
				preview.SnapshotComplete = false
			}
			if item.RateVersionID == nil || item.AnnualRateBps == nil {
				preview.RateCoverageComplete = false
			}
			if item.Denominator != BigIntString(big.NewInt(DailyDenominator)) || item.ClosingCarryNumerator == "" || item.OpeningCarryNumerator == "" {
				preview.CarryChainContinuous = false
			}
			if previous, ok := lastCarry[item.EnrollmentID]; ok && previous != item.OpeningCarryNumerator {
				preview.CarryChainContinuous = false
			}
			lastCarry[item.EnrollmentID] = item.ClosingCarryNumerator
		}
		switch item.Status {
		case model.InterestAccrualCompletedZero:
			preview.ZeroAccruals++
		case model.InterestAccrualCompletedPosted, model.InterestAccrualAdjusted:
			if item.RecognizedAmount != nil {
				preview.PostedLiability += *item.RecognizedAmount
			}
			if item.Status == model.InterestAccrualCompletedPosted && item.RecognizedAmount != nil && *item.RecognizedAmount > 0 && item.LedgerTransactionID == nil {
				preview.MissingItems++
			}
		case model.InterestAccrualBlocked, model.InterestAccrualFailed:
			preview.BlockedItems++
		default:
			preview.MissingItems++
		}
	}
	if !preview.SnapshotComplete {
		preview.MissingItems++
	}
	if !preview.RateCoverageComplete {
		preview.MissingItems++
	}
	if !preview.CarryChainContinuous {
		preview.MissingItems++
	}
	if int64(len(accruals)) < period.ExpectedItemCount {
		preview.MissingItems += int(period.ExpectedItemCount - int64(len(accruals)))
	}
	if period.Status != model.InterestPeriodClosed {
		inventoryStatus := "pass"
		if preview.BlockedItems > 0 || preview.MissingItems > 0 || preview.AccountCloseBlocked || !preview.SnapshotComplete || !preview.CarryChainContinuous || !preview.RateCoverageComplete {
			inventoryStatus = "fail"
		}
		if err := s.repo.PutPeriodCheck(ctx, model.InterestPeriodCheck{
			ID: identifiers.NewV7(), PeriodID: period.ID, CheckName: "daily_accrual_inventory",
			Status: inventoryStatus, ExpectedValue: new(fmt.Sprintf("%d", period.ExpectedItemCount)),
			ActualValue: new(fmt.Sprintf("%d", len(accruals))), Severity: "critical",
			Details: nil, CheckedAt: time.Now().UTC(),
		}); err != nil {
			return PeriodPreview{}, err
		}
		liabilityStatus := "pass"
		if period.TotalAccruedAmount != preview.PostedLiability {
			liabilityStatus = "fail"
		}
		if err := s.repo.PutPeriodCheck(ctx, model.InterestPeriodCheck{
			ID: identifiers.NewV7(), PeriodID: period.ID, CheckName: "liability_reconciliation",
			Status: liabilityStatus, ExpectedValue: new(fmt.Sprintf("%d", period.TotalAccruedAmount)),
			ActualValue: new(fmt.Sprintf("%d", preview.PostedLiability)), Severity: "critical",
			Details: nil, CheckedAt: time.Now().UTC(),
		}); err != nil {
			return PeriodPreview{}, err
		}
	}
	previousClosed := true
	if period.Status != model.InterestPeriodClosed {
		previousClosed, err = s.repo.IsPreviousPeriodClosed(ctx, period.ID)
		if err != nil {
			return PeriodPreview{}, err
		}
		previousStatus := "pass"
		if !previousClosed {
			previousStatus = "fail"
		}
		if err := s.repo.PutPeriodCheck(ctx, model.InterestPeriodCheck{
			ID: identifiers.NewV7(), PeriodID: period.ID, CheckName: "previous_period_closed",
			Status: previousStatus, ExpectedValue: new("true"),
			ActualValue: new(fmt.Sprintf("%t", previousClosed)), Severity: "critical",
			CheckedAt: time.Now().UTC(),
		}); err != nil {
			return PeriodPreview{}, err
		}
	}
	if preview.BlockedItems == 0 && preview.MissingItems == 0 && !preview.AccountCloseBlocked && preview.SnapshotComplete && preview.CarryChainContinuous && preview.RateCoverageComplete && period.Status != model.InterestPeriodClosed {
		if !previousClosed {
			preview.Reason = "previous period is not closed"
			return preview, nil
		}
		if time.Now().UTC().Before(period.CloseNotBeforeAt.UTC()) {
			preview.Reason = "close window has not opened"
			return preview, nil
		}
		preview.Ready = true
		preview.ExpectedCapitalization = preview.PostedLiability
	} else if period.Status == model.InterestPeriodClosed {
		preview.Reason = "period is already closed"
	} else if preview.AccountCloseBlocked {
		preview.Reason = "an enrolled account is not active"
	} else {
		preview.Reason = "daily accrual inventory is incomplete"
	}
	return preview, nil
}

func (s *Service) ClosePeriod(ctx context.Context, periodID uuid.UUID, actor string) error {
	if actor == "" {
		return fmt.Errorf("%w: close actor is required", apperror.ErrValidation)
	}
	current, err := s.repo.GetPeriod(ctx, periodID)
	if err != nil {
		return err
	}
	if current.Status == model.InterestPeriodClosed {
		return ErrClosedPeriodImmutable
	}
	preview, err := s.PreviewPeriodClose(ctx, periodID)
	if err != nil {
		return err
	}
	if !preview.Ready {
		return fmt.Errorf("%w: %s", ErrPeriodNotReady, preview.Reason)
	}
	period, err := s.repo.GetPeriod(ctx, periodID)
	if err != nil {
		return err
	}
	if err := s.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		return s.repo.MarkPeriodStatus(ctx, tx, periodID, model.InterestPeriodClosing, "")
	}); err != nil {
		return err
	}
	failClose := func(cause error) error {
		failErr := s.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
			return s.repo.MarkPeriodStatus(ctx, tx, periodID, model.InterestPeriodFailed, "INTEREST_PERIOD_CLOSE_FAILED")
		})
		if failErr != nil {
			return fmt.Errorf("%w; record failed period: %v", cause, failErr)
		}
		return cause
	}
	if err := s.repo.EnsureCapitalizationItems(ctx, periodID); err != nil {
		return failClose(err)
	}
	items, err := s.repo.ListCapitalizationItems(ctx, periodID)
	if err != nil {
		return failClose(err)
	}
	for _, item := range items {
		if item.Status == model.InterestCapitalizationPosted || item.Status == model.InterestCapitalizationCompletedZero || item.Status == model.InterestCapitalizationAdjusted {
			continue
		}
		if item.CapitalizationAmount == 0 {
			if err := s.completeCapitalization(ctx, period, item, model.InterestCapitalizationCompletedZero, nil); err != nil {
				return failClose(err)
			}
			continue
		}
		claimed, claimErr := s.repo.StartCapitalization(ctx, item.ID, "close-"+uuid.NewString(), time.Now().UTC())
		if claimErr != nil {
			// Another close worker may have claimed this item between the list
			// and the lease update. Leave it for that worker; the final inventory
			// check below will keep this period retryable if it has not completed.
			if errors.Is(claimErr, sql.ErrNoRows) {
				continue
			}
			return failClose(claimErr)
		}
		key := "interest-capitalize:" + claimed.EnrollmentID.String() + ":" + period.ID.String()
		postErr := commandservice.Run(ctx, s.poster, processors.Command{
			IdempotencyKey: key, IdempotencyScope: period.ID.String(), Type: "interest_capitalize",
			Amount: decimal.NewFromInt(claimed.CapitalizationAmount), Metadata: map[string]any{
				"account_id": claimed.AccountID.String(), "enrollment_id": claimed.EnrollmentID.String(), "period_id": period.ID.String(), "close_actor": actor,
			},
		}, commandservice.ExecutionContext{Source: "internal-worker", CorrelationID: key, RequestOrigin: "interest-period-close"})
		if postErr != nil && !errors.Is(postErr, apperror.ErrAlreadyPosted) {
			status := model.InterestCapitalizationRetryWait
			nextAttempt := time.Now().UTC().Add(5 * time.Minute)
			if isBusinessFailure(postErr) {
				status = model.InterestCapitalizationBlocked
				nextAttempt = time.Time{}
			}
			if failErr := s.repo.FailCapitalization(ctx, claimed.ID, status, "INTEREST_CAPITALIZATION_POST_FAILED", nextAttempt); failErr != nil {
				return failClose(failErr)
			}
			return failClose(postErr)
		}
		var txID *uuid.UUID
		if s.txLookup != nil {
			tx, lookupErr := s.txLookup.GetTransactionByIdempotencyKey(ctx, key, period.ID.String())
			if lookupErr != nil {
				if failErr := s.repo.FailCapitalization(ctx, claimed.ID, model.InterestCapitalizationRetryWait,
					"INTEREST_TRANSACTION_LOOKUP_FAILED", time.Now().UTC().Add(5*time.Minute)); failErr != nil {
					return failClose(failErr)
				}
				return failClose(lookupErr)
			}
			txID = &tx.ID
		}
		if err := s.completeCapitalization(ctx, period, claimed, model.InterestCapitalizationPosted, txID); err != nil {
			return failClose(err)
		}
	}
	finalItems, err := s.repo.ListCapitalizationItems(ctx, periodID)
	if err != nil {
		return failClose(err)
	}
	for _, item := range finalItems {
		if item.Status != model.InterestCapitalizationPosted && item.Status != model.InterestCapitalizationCompletedZero && item.Status != model.InterestCapitalizationAdjusted {
			return failClose(fmt.Errorf("%w: capitalization item %s is %s", ErrPeriodNotReady, item.ID, item.Status))
		}
		if item.Status == model.InterestCapitalizationPosted && item.CapitalizationAmount > 0 && item.LedgerTransactionID == nil {
			return failClose(fmt.Errorf("%w: capitalization item %s has no ledger transaction", ErrPeriodNotReady, item.ID))
		}
	}
	finalPeriod, err := s.repo.GetPeriod(ctx, periodID)
	if err != nil {
		return failClose(err)
	}
	if finalPeriod.TotalCapitalizedAmount != finalPeriod.TotalAccruedAmount {
		return failClose(fmt.Errorf("%w: accrued=%d capitalized=%d", ErrPeriodNotReady, finalPeriod.TotalAccruedAmount, finalPeriod.TotalCapitalizedAmount))
	}
	if err := s.repo.PutPeriodCheck(ctx, model.InterestPeriodCheck{
		ID: identifiers.NewV7(), PeriodID: periodID, CheckName: "capitalization_reconciliation",
		Status: "pass", ExpectedValue: new(fmt.Sprintf("%d", finalPeriod.TotalAccruedAmount)),
		ActualValue: new(fmt.Sprintf("%d", finalPeriod.TotalCapitalizedAmount)), Severity: "critical",
		CheckedAt: time.Now().UTC(),
	}); err != nil {
		return failClose(err)
	}
	closedAt := time.Now().UTC()
	return s.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		if err := s.repo.MarkPeriodStatus(ctx, tx, periodID, model.InterestPeriodClosed, ""); err != nil {
			return err
		}
		return s.insertOutbox(ctx, tx, model.OutboxEvent{
			AggregateType: "interest_period",
			AggregateID:   periodID,
			EventType:     events.TypeInterestPeriodClosed,
			Payload: events.NewInterestPeriodClosed(periodID, period.ProductID, period.Currency,
				period.PeriodYear, period.PeriodMonth, fmt.Sprintf("%d", finalPeriod.TotalAccruedAmount),
				fmt.Sprintf("%d", finalPeriod.TotalCapitalizedAmount), closedAt).ToPayload(),
		})
	})
}

func (s *Service) completeCapitalization(ctx context.Context, period model.InterestPeriod, item model.InterestCapitalizationItem, status string, transactionID *uuid.UUID) error {
	return s.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		if err := s.repo.CompleteCapitalization(ctx, tx, item.ID, status, transactionID); err != nil {
			return err
		}
		return s.insertOutbox(ctx, tx, model.OutboxEvent{
			AggregateType: "interest_capitalization",
			AggregateID:   item.ID,
			EventType:     events.TypeInterestCapitalized,
			Payload: events.NewInterestCapitalized(item.ID, item.PeriodID, item.EnrollmentID,
				item.AccountID, fmt.Sprintf("%d", item.CapitalizationAmount), period.Currency,
				transactionID, time.Now().UTC()).ToPayload(),
		})
	})
}

// CloseDuePeriods is the worker-facing close sweep. Each period is isolated
// so one blocked product period cannot prevent another currency/product from
// being evaluated and recorded.
func (s *Service) CloseDuePeriods(ctx context.Context, now time.Time, actor string) (closed, failed int, err error) {
	periods, err := s.repo.ListDuePeriods(ctx, now)
	if err != nil {
		return 0, 0, err
	}
	for _, period := range periods {
		if closeErr := s.ClosePeriod(ctx, period.ID, actor); closeErr != nil {
			failed++
			s.logger.Error("interest: period close failed", "period_id", period.ID.String(), "error", closeErr)
			continue
		}
		closed++
	}
	return closed, failed, nil
}

func (s *Service) CreateAdjustment(ctx context.Context, adjustment model.InterestAdjustment) (model.InterestAdjustment, error) {
	if adjustment.SourcePeriodID == uuid.Nil || adjustment.EnrollmentID == uuid.Nil {
		return model.InterestAdjustment{}, fmt.Errorf("%w: adjustment period and enrollment are required", apperror.ErrValidation)
	}
	if adjustment.Direction != "positive" && adjustment.Direction != "negative" {
		return model.InterestAdjustment{}, fmt.Errorf("%w: adjustment direction must be positive or negative", apperror.ErrValidation)
	}
	if adjustment.SourceAccrualID == nil && adjustment.SourceCapitalizationID == nil {
		return model.InterestAdjustment{}, fmt.Errorf("%w: adjustment must link an accrual or capitalization item", apperror.ErrValidation)
	}
	if adjustment.SourceAccrualID != nil && adjustment.SourceCapitalizationID != nil {
		return model.InterestAdjustment{}, fmt.Errorf("%w: adjustment must link exactly one source item", apperror.ErrValidation)
	}
	period, err := s.repo.GetPeriod(ctx, adjustment.SourcePeriodID)
	if err != nil {
		return model.InterestAdjustment{}, err
	}
	if period.Status != model.InterestPeriodClosed {
		return model.InterestAdjustment{}, fmt.Errorf("%w: corrections require a closed source period", apperror.ErrValidation)
	}
	return s.repo.CreateAdjustment(ctx, adjustment)
}

func (s *Service) ApproveAdjustment(ctx context.Context, id uuid.UUID, checker string) error {
	if checker == "" {
		return fmt.Errorf("%w: adjustment checker is required", apperror.ErrValidation)
	}
	adjustment, err := s.repo.GetAdjustment(ctx, id)
	if err != nil {
		return err
	}
	if adjustment.Status == "posted" {
		return nil
	}
	if adjustment.Status == "pending_approval" {
		if err := s.repo.ApproveAdjustment(ctx, id, checker); err != nil {
			return err
		}
		adjustment, err = s.repo.GetAdjustment(ctx, id)
		if err != nil {
			return err
		}
	}
	if adjustment.Status != "approved" {
		return fmt.Errorf("%w: adjustment is not approved", apperror.ErrValidation)
	}
	enrollment, err := s.repo.GetEnrollment(ctx, adjustment.EnrollmentID)
	if err != nil {
		return err
	}
	product, err := s.repo.GetProduct(ctx, enrollment.ProductID)
	if err != nil {
		return err
	}
	key := "interest-adjustment:" + adjustment.ID.String()
	postErr := commandservice.Run(ctx, s.poster, processors.Command{
		IdempotencyKey: key, IdempotencyScope: "interest-adjustment:" + adjustment.ID.String(),
		Type: "interest_adjustment", Amount: decimal.NewFromInt(adjustment.Amount), UserID: enrollment.UserID,
		Metadata: map[string]any{
			"account_id": enrollment.AccountID.String(), "adjustment_id": adjustment.ID.String(),
			"period_id": adjustment.SourcePeriodID.String(), "enrollment_id": adjustment.EnrollmentID.String(),
			"direction": adjustment.Direction, "reason": adjustment.Reason,
			"correction_stage": adjustmentStage(adjustment),
		},
	}, commandservice.ExecutionContext{Source: "internal-worker", CorrelationID: key, RequestOrigin: "interest-adjustment-approval"})
	if postErr != nil && !errors.Is(postErr, apperror.ErrAlreadyPosted) {
		return postErr
	}
	var txID *uuid.UUID
	if s.txLookup != nil {
		tx, lookupErr := s.txLookup.GetTransactionByIdempotencyKey(ctx, key, "interest-adjustment:"+adjustment.ID.String())
		if lookupErr != nil {
			return lookupErr
		}
		txID = &tx.ID
	}
	if marker, ok := s.repo.(interface {
		MarkAdjustmentPostedTx(context.Context, *sql.Tx, uuid.UUID, *uuid.UUID) error
	}); ok {
		return s.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
			if err := marker.MarkAdjustmentPostedTx(ctx, tx, id, txID); err != nil {
				return err
			}
			return s.insertOutbox(ctx, tx, model.OutboxEvent{
				AggregateType: "interest_adjustment",
				AggregateID:   adjustment.ID,
				EventType:     events.TypeInterestAdjusted,
				Payload: events.NewInterestAdjusted(adjustment.ID, adjustment.SourcePeriodID,
					adjustment.EnrollmentID, fmt.Sprintf("%d", adjustment.Amount), adjustment.Direction,
					adjustmentStage(adjustment), product.Currency, txID, time.Now().UTC()).ToPayload(),
			})
		})
	}
	if err := s.repo.MarkAdjustmentPosted(ctx, id, txID); err != nil {
		return err
	}
	// Compatibility repositories that predate the transactional helper still
	// complete their state transition correctly.  Their optional outbox path is
	// necessarily best-effort because the old interface cannot share its tx.
	if s.outbox == nil {
		return nil
	}
	return s.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		return s.insertOutbox(ctx, tx, model.OutboxEvent{
			AggregateType: "interest_adjustment",
			AggregateID:   adjustment.ID,
			EventType:     events.TypeInterestAdjusted,
			Payload: events.NewInterestAdjusted(adjustment.ID, adjustment.SourcePeriodID,
				adjustment.EnrollmentID, fmt.Sprintf("%d", adjustment.Amount), adjustment.Direction,
				adjustmentStage(adjustment), product.Currency, txID, time.Now().UTC()).ToPayload(),
		})
	})
}

func NewAdjustmentID() uuid.UUID { return identifiers.NewV7() }

func adjustmentStage(adjustment model.InterestAdjustment) string {
	if adjustment.SourceAccrualID != nil {
		return "accrual"
	}
	return "capitalization"
}
