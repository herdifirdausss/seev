package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/herdifirdausss/seev/internal/platform/database"
	"github.com/herdifirdausss/seev/internal/platform/database/identifiers"
	"github.com/herdifirdausss/seev/services/ledger/internal/ledger/errors"
	"github.com/herdifirdausss/seev/services/ledger/internal/ledger/model"
)

// InterestRepository is the Ledger-owned persistence boundary for the
// product/rate/enrollment and period-close records.  It is deliberately
// separate from the legacy SavingsRepository so old daily-capitalisation
// callers remain source-compatible during the expand/contract rollout.
type InterestRepository interface {
	CreateProduct(context.Context, model.SavingsProduct) (model.SavingsProduct, error)
	GetProduct(context.Context, uuid.UUID) (model.SavingsProduct, error)
	ListProducts(context.Context, string) ([]model.SavingsProduct, error)
	UpdateProductStatus(context.Context, uuid.UUID, string, string) error
	CreateRate(context.Context, model.SavingsRateVersion) (model.SavingsRateVersion, error)
	SubmitRate(context.Context, uuid.UUID, string) error
	ApproveRate(context.Context, uuid.UUID, string) error
	RejectRate(context.Context, uuid.UUID, string, string) error
	GetRateForDate(context.Context, uuid.UUID, time.Time) (model.SavingsRateVersion, error)
	CreateEnrollment(context.Context, model.SavingsEnrollment) (model.SavingsEnrollment, error)
	GetEnrollment(context.Context, uuid.UUID) (model.SavingsEnrollment, error)
	ListEnrollments(context.Context, uuid.UUID) ([]model.SavingsEnrollment, error)
	ListActiveEnrollments(context.Context, time.Time) ([]model.SavingsEnrollment, error)
	ListEnrollmentAccruals(context.Context, uuid.UUID) ([]model.InterestDailyAccrual, error)
	ListEnrollmentPeriods(context.Context, uuid.UUID) ([]model.InterestPeriod, error)
	ListEnrollmentCapitalizations(context.Context, uuid.UUID) ([]model.InterestCapitalizationItem, error)
	EnsurePeriod(context.Context, model.InterestPeriod) (model.InterestPeriod, error)
	GetPeriod(context.Context, uuid.UUID) (model.InterestPeriod, error)
	IsPreviousPeriodClosed(context.Context, uuid.UUID) (bool, error)
	ListDuePeriods(context.Context, time.Time) ([]model.InterestPeriod, error)
	RefreshExpectedItemCount(context.Context, uuid.UUID) error
	CountEligibleEnrollments(context.Context, uuid.UUID) (int, error)
	HasNonActiveCapitalizationAccount(context.Context, uuid.UUID) (bool, error)
	MarkPeriodStatus(context.Context, *sql.Tx, uuid.UUID, string, string) error
	CreateOrGetDailyAccrual(context.Context, model.InterestDailyAccrual) (model.InterestDailyAccrual, error)
	ClaimDailyAccrualForID(context.Context, uuid.UUID, string, time.Time) (model.InterestDailyAccrual, error)
	ClaimDailyAccrual(context.Context, string, time.Time) (model.InterestDailyAccrual, error)
	GetCarryBeforeDate(context.Context, uuid.UUID, time.Time) (string, string, bool, error)
	SaveDailyCalculation(context.Context, *sql.Tx, model.InterestDailyAccrual) error
	CompleteDailyAccrual(context.Context, *sql.Tx, uuid.UUID, string, *uuid.UUID, string, string) error
	FailDailyAccrual(context.Context, uuid.UUID, string, string, time.Time) error
	ListPeriodAccruals(context.Context, uuid.UUID) ([]model.InterestDailyAccrual, error)
	EnsureCapitalizationItems(context.Context, uuid.UUID) error
	ListCapitalizationItems(context.Context, uuid.UUID) ([]model.InterestCapitalizationItem, error)
	StartCapitalization(context.Context, uuid.UUID, string, time.Time) (model.InterestCapitalizationItem, error)
	CompleteCapitalization(context.Context, *sql.Tx, uuid.UUID, string, *uuid.UUID) error
	FailCapitalization(context.Context, uuid.UUID, string, string, time.Time) error
	PutPeriodCheck(context.Context, model.InterestPeriodCheck) error
	CreateAdjustment(context.Context, model.InterestAdjustment) (model.InterestAdjustment, error)
	GetAdjustment(context.Context, uuid.UUID) (model.InterestAdjustment, error)
	ApproveAdjustment(context.Context, uuid.UUID, string) error
	MarkAdjustmentPosted(context.Context, uuid.UUID, *uuid.UUID) error
}

type interestRepo struct{ db database.DatabaseSQL }

func NewInterestRepository(db database.DatabaseSQL) InterestRepository {
	return &interestRepo{db: db}
}

func (r *interestRepo) CreateProduct(ctx context.Context, product model.SavingsProduct) (model.SavingsProduct, error) {
	if product.ID == uuid.Nil {
		product.ID = identifiers.NewV7()
	}
	if product.PublicID == "" {
		product.PublicID = product.ID.String()
	}
	if product.DayCountConvention == "" {
		product.DayCountConvention = "ACT/365F"
	}
	if len(product.EligibleAccountTypes) == 0 {
		product.EligibleAccountTypes = []string{"cash", "pocket"}
	}
	if product.Status == "" {
		product.Status = model.SavingsProductDraft
	}
	if product.CapitalizationFrequency == "" {
		product.CapitalizationFrequency = "monthly"
	}
	if product.Timezone == "" {
		product.Timezone = "Asia/Jakarta"
	}
	if product.DefaultRatePolicy == "" {
		product.DefaultRatePolicy = "effective_rate_version"
	}
	if product.Version == 0 {
		product.Version = 1
	}
	if product.CreatedAt.IsZero() {
		product.CreatedAt = time.Now().UTC()
	}
	if product.UpdatedAt.IsZero() {
		product.UpdatedAt = product.CreatedAt
	}
	err := r.db.QueryRowContext(ctx, `
		INSERT INTO savings_products
			(id, public_id, product_code, name, currency, eligible_account_types,
			 status, day_count_convention, capitalization_frequency, timezone,
			 minimum_eligible_balance, interest_expense_account_id,
			 interest_payable_account_id, default_rate_policy, version,
			 created_by, updated_by, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)
		RETURNING `+savingsProductColumns, product.ID, product.PublicID, product.ProductCode,
		product.Name, product.Currency, pq.Array(product.EligibleAccountTypes), product.Status,
		product.DayCountConvention, product.CapitalizationFrequency, product.Timezone,
		product.MinimumEligibleBalance, product.InterestExpenseAccountID,
		product.InterestPayableAccountID, product.DefaultRatePolicy, product.Version,
		product.CreatedBy, product.UpdatedBy, product.CreatedAt, product.UpdatedAt).Scan(productScanArgs(&product)...)
	if err != nil {
		return model.SavingsProduct{}, fmt.Errorf("create savings product: %w", err)
	}
	return product, nil
}

const savingsProductColumns = `id, public_id, product_code, name, currency,
eligible_account_types, status, day_count_convention, capitalization_frequency,
timezone, minimum_eligible_balance, interest_expense_account_id,
interest_payable_account_id, default_rate_policy, version, created_by, updated_by,
created_at, updated_at`

func productScanArgs(p *model.SavingsProduct) []any {
	return []any{&p.ID, &p.PublicID, &p.ProductCode, &p.Name, &p.Currency,
		pq.Array(&p.EligibleAccountTypes), &p.Status, &p.DayCountConvention,
		&p.CapitalizationFrequency, &p.Timezone, &p.MinimumEligibleBalance,
		&p.InterestExpenseAccountID, &p.InterestPayableAccountID, &p.DefaultRatePolicy,
		&p.Version, &p.CreatedBy, &p.UpdatedBy, &p.CreatedAt, &p.UpdatedAt}
}

func (r *interestRepo) GetProduct(ctx context.Context, id uuid.UUID) (model.SavingsProduct, error) {
	var product model.SavingsProduct
	err := r.db.QueryRowContext(ctx, `SELECT `+savingsProductColumns+` FROM savings_products WHERE id=$1`, id).Scan(productScanArgs(&product)...)
	if errors.Is(err, sql.ErrNoRows) {
		return model.SavingsProduct{}, fmt.Errorf("%w: savings product %s", apperror.ErrAccountNotFound, id)
	}
	if err != nil {
		return model.SavingsProduct{}, fmt.Errorf("get savings product: %w", err)
	}
	return product, nil
}

func (r *interestRepo) ListProducts(ctx context.Context, status string) ([]model.SavingsProduct, error) {
	query := `SELECT ` + savingsProductColumns + ` FROM savings_products`
	args := []any{}
	if status != "" {
		query += ` WHERE status=$1`
		args = append(args, status)
	}
	query += ` ORDER BY product_code`
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list savings products: %w", err)
	}
	defer rows.Close()
	result := make([]model.SavingsProduct, 0)
	for rows.Next() {
		var product model.SavingsProduct
		if err := rows.Scan(productScanArgs(&product)...); err != nil {
			return nil, fmt.Errorf("scan savings product: %w", err)
		}
		result = append(result, product)
	}
	return result, rows.Err()
}

func (r *interestRepo) UpdateProductStatus(ctx context.Context, id uuid.UUID, status, checker string) error {
	result, err := r.db.ExecContext(ctx, `UPDATE savings_products SET
		status=$2,updated_by=$3,version=version+1,updated_at=now()
		WHERE id=$1 AND created_by<>$3 AND (
			($2='active' AND status IN ('draft','intake_paused')) OR
			($2='intake_paused' AND status='active') OR
			($2='retired' AND status IN ('active','intake_paused'))
		)`, id, status, checker)
	if err != nil {
		return fmt.Errorf("update savings product status: %w", err)
	}
	return requireRows(result, "update savings product status")
}

const savingsRateColumns = `id, public_id, product_id, annual_rate_bps, status,
effective_from, effective_until, content_hash, created_by, submitted_by,
approved_by, rejected_by, created_at, submitted_at, approved_at, retired_at,
rejection_reason`

func rateScanArgs(rate *model.SavingsRateVersion) []any {
	return []any{&rate.ID, &rate.PublicID, &rate.ProductID, &rate.AnnualRateBps,
		&rate.Status, &rate.EffectiveFrom, &rate.EffectiveUntil, &rate.ContentHash,
		&rate.CreatedBy, &rate.SubmittedBy, &rate.ApprovedBy, &rate.RejectedBy,
		&rate.CreatedAt, &rate.SubmittedAt, &rate.ApprovedAt, &rate.RetiredAt,
		&rate.RejectionReason}
}

func (r *interestRepo) CreateRate(ctx context.Context, rate model.SavingsRateVersion) (model.SavingsRateVersion, error) {
	if rate.ID == uuid.Nil {
		rate.ID = identifiers.NewV7()
	}
	if rate.PublicID == "" {
		rate.PublicID = rate.ID.String()
	}
	if len(rate.ContentHash) == 0 {
		rate.ContentHash = []byte(rate.PublicID)
	}
	err := r.db.QueryRowContext(ctx, `
		INSERT INTO savings_rate_versions
		(id,public_id,product_id,annual_rate_bps,status,effective_from,effective_until,
		 content_hash,created_by)
		VALUES ($1,$2,$3,$4,COALESCE(NULLIF($5,''),'draft'),$6,$7,$8,$9)
		RETURNING `+savingsRateColumns, rate.ID, rate.PublicID, rate.ProductID,
		rate.AnnualRateBps, rate.Status, rate.EffectiveFrom, rate.EffectiveUntil,
		rate.ContentHash, rate.CreatedBy).Scan(rateScanArgs(&rate)...)
	if err != nil {
		return model.SavingsRateVersion{}, fmt.Errorf("create savings rate: %w", err)
	}
	return rate, nil
}

func (r *interestRepo) SubmitRate(ctx context.Context, id uuid.UUID, submittedBy string) error {
	result, err := r.db.ExecContext(ctx, `UPDATE savings_rate_versions
		SET status='pending_approval', submitted_by=$2, submitted_at=now()
		WHERE id=$1 AND status='draft'`, id, submittedBy)
	if err != nil {
		return fmt.Errorf("submit savings rate: %w", err)
	}
	return requireRows(result, "submit savings rate")
}

func (r *interestRepo) ApproveRate(ctx context.Context, id uuid.UUID, checker string) error {
	return r.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		var creator string
		var productID uuid.UUID
		var productStatus string
		var from time.Time
		var until *time.Time
		if err := tx.QueryRowContext(ctx, `SELECT r.created_by, r.product_id, r.effective_from, r.effective_until, p.status
			FROM savings_rate_versions r JOIN savings_products p ON p.id=r.product_id
			WHERE r.id=$1 AND r.status='pending_approval' FOR UPDATE`, id).
			Scan(&creator, &productID, &from, &until, &productStatus); err != nil {
			return fmt.Errorf("load savings rate for approval: %w", err)
		}
		if creator == checker {
			return fmt.Errorf("%w: rate maker and checker must differ", apperror.ErrValidation)
		}
		if productStatus == model.SavingsProductRetired {
			return fmt.Errorf("%w: retired savings products cannot activate rates", apperror.ErrValidation)
		}
		var overlap bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (
			SELECT 1 FROM savings_rate_versions
			WHERE product_id=$1 AND id<>$2 AND status='active'
			AND effective_from < COALESCE($4::date,'9999-12-31'::date)
			AND COALESCE(effective_until,'9999-12-31'::date) > $3::date
		)`, productID, id, from, until).Scan(&overlap); err != nil {
			return fmt.Errorf("check savings rate overlap: %w", err)
		}
		if overlap {
			return fmt.Errorf("%w: active savings rate window overlaps", apperror.ErrValidation)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE savings_rate_versions
			SET status='active', approved_by=$2, approved_at=now()
			WHERE id=$1`, id, checker); err != nil {
			return fmt.Errorf("approve savings rate: %w", err)
		}
		return nil
	})
}

func (r *interestRepo) RejectRate(ctx context.Context, id uuid.UUID, checker, reason string) error {
	result, err := r.db.ExecContext(ctx, `UPDATE savings_rate_versions
		SET status='rejected', rejected_by=$2, rejection_reason=$3
		WHERE id=$1 AND status='pending_approval' AND created_by<>$2`, id, checker, reason)
	if err != nil {
		return fmt.Errorf("reject savings rate: %w", err)
	}
	return requireRows(result, "reject savings rate")
}

func (r *interestRepo) GetRateForDate(ctx context.Context, productID uuid.UUID, date time.Time) (model.SavingsRateVersion, error) {
	var rate model.SavingsRateVersion
	err := r.db.QueryRowContext(ctx, `SELECT `+savingsRateColumns+` FROM savings_rate_versions
		WHERE product_id=$1 AND status='active' AND effective_from <= $2::date
		AND (effective_until IS NULL OR effective_until > $2::date)
		ORDER BY effective_from DESC, id DESC LIMIT 1`, productID, date.Format("2006-01-02")).Scan(rateScanArgs(&rate)...)
	if err != nil {
		return model.SavingsRateVersion{}, fmt.Errorf("get savings rate for %s: %w", date.Format("2006-01-02"), err)
	}
	return rate, nil
}

const savingsEnrollmentColumns = `id, public_id, product_id, account_id, user_id,
status, mode, effective_from, effective_until, carry_numerator, carry_denominator,
version, created_by, updated_by, created_at, updated_at`

func savingsEnrollmentColumnsWithAlias(alias string) string {
	return alias + ".id," + alias + ".public_id," + alias + ".product_id," + alias + ".account_id," + alias + ".user_id," +
		alias + ".status," + alias + ".mode," + alias + ".effective_from," + alias + ".effective_until," +
		alias + ".carry_numerator," + alias + ".carry_denominator," + alias + ".version," + alias + ".created_by," +
		alias + ".updated_by," + alias + ".created_at," + alias + ".updated_at"
}

func enrollmentScanArgs(enrollment *model.SavingsEnrollment) []any {
	return []any{&enrollment.ID, &enrollment.PublicID, &enrollment.ProductID,
		&enrollment.AccountID, &enrollment.UserID, &enrollment.Status, &enrollment.Mode,
		&enrollment.EffectiveFrom, &enrollment.EffectiveUntil, &enrollment.CarryNumerator,
		&enrollment.CarryDenominator, &enrollment.Version, &enrollment.CreatedBy,
		&enrollment.UpdatedBy, &enrollment.CreatedAt, &enrollment.UpdatedAt}
}

func (r *interestRepo) CreateEnrollment(ctx context.Context, enrollment model.SavingsEnrollment) (model.SavingsEnrollment, error) {
	if enrollment.ID == uuid.Nil {
		enrollment.ID = identifiers.NewV7()
	}
	if enrollment.PublicID == "" {
		enrollment.PublicID = enrollment.ID.String()
	}
	if enrollment.Mode == "" {
		enrollment.Mode = "monthly_liability_capitalization"
	}
	if enrollment.CarryNumerator == "" {
		enrollment.CarryNumerator = "0"
	}
	if enrollment.CarryDenominator == "" {
		enrollment.CarryDenominator = "3650000"
	}
	if enrollment.Version == 0 {
		enrollment.Version = 1
	}
	err := r.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, `INSERT INTO savings_enrollments
			(id,public_id,product_id,account_id,user_id,status,mode,effective_from,effective_until,
			 carry_numerator,carry_denominator,version,created_by,updated_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
			RETURNING `+savingsEnrollmentColumns, enrollment.ID, enrollment.PublicID,
			enrollment.ProductID, enrollment.AccountID, enrollment.UserID, enrollment.Status,
			enrollment.Mode, enrollment.EffectiveFrom, enrollment.EffectiveUntil,
			enrollment.CarryNumerator, enrollment.CarryDenominator, enrollment.Version,
			enrollment.CreatedBy, enrollment.UpdatedBy).Scan(enrollmentScanArgs(&enrollment)...); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO savings_enrollment_status_history
			(id,enrollment_id,status,effective_from,changed_by)
			VALUES ($1,$2,$3,$4,$5)`, identifiers.NewV7(), enrollment.ID,
			enrollment.Status, enrollment.EffectiveFrom.Format("2006-01-02"), enrollment.CreatedBy)
		return err
	})
	if err != nil {
		return model.SavingsEnrollment{}, fmt.Errorf("create savings enrollment: %w", err)
	}
	return enrollment, nil
}

func (r *interestRepo) GetEnrollment(ctx context.Context, id uuid.UUID) (model.SavingsEnrollment, error) {
	var enrollment model.SavingsEnrollment
	err := r.db.QueryRowContext(ctx, `SELECT `+savingsEnrollmentColumns+` FROM savings_enrollments WHERE id=$1`, id).Scan(enrollmentScanArgs(&enrollment)...)
	if errors.Is(err, sql.ErrNoRows) {
		return model.SavingsEnrollment{}, fmt.Errorf("%w: savings enrollment %s", apperror.ErrSavingsConfigNotFound, id)
	}
	if err != nil {
		return model.SavingsEnrollment{}, fmt.Errorf("get savings enrollment: %w", err)
	}
	return enrollment, nil
}

func (r *interestRepo) ListEnrollments(ctx context.Context, userID uuid.UUID) ([]model.SavingsEnrollment, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+savingsEnrollmentColumns+` FROM savings_enrollments WHERE user_id=$1 ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list savings enrollments: %w", err)
	}
	defer rows.Close()
	result := make([]model.SavingsEnrollment, 0)
	for rows.Next() {
		var enrollment model.SavingsEnrollment
		if err := rows.Scan(enrollmentScanArgs(&enrollment)...); err != nil {
			return nil, fmt.Errorf("scan savings enrollment: %w", err)
		}
		result = append(result, enrollment)
	}
	return result, rows.Err()
}

// UpdateEnrollmentStatus is intentionally kept off InterestRepository so
// existing repository fakes remain source-compatible while the lifecycle API
// is rolled out.  The database predicate is the final maker/checker and
// state-transition guard.
func (r *interestRepo) UpdateEnrollmentStatus(ctx context.Context, id uuid.UUID, status, actor string, effectiveUntil *time.Time) error {
	return r.UpdateEnrollmentStatusWithEffectiveDate(ctx, id, status, actor, nil, effectiveUntil)
}

// UpdateEnrollmentStatusWithEffectiveDate advances the operational enrollment
// projection and appends the calendar-effective lifecycle change in the same
// transaction.  The history row is upserted for same-day operator changes so
// the final state for that day is the state period close evaluates.
func (r *interestRepo) UpdateEnrollmentStatusWithEffectiveDate(ctx context.Context, id uuid.UUID, status, actor string, effectiveFrom, effectiveUntil *time.Time) error {
	if effectiveFrom == nil || effectiveFrom.IsZero() {
		now := time.Now().UTC()
		effectiveFrom = &now
	}
	var untilArg any
	if effectiveUntil != nil {
		// Pass a date-only value: the enrollment boundary is in the
		// product's local calendar, not the database session timezone.
		untilArg = effectiveUntil.Format("2006-01-02")
	}
	err := r.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE savings_enrollments
			SET status=$2,
				effective_until=CASE
					WHEN $2='ended' AND $4::date IS NOT NULL
						THEN LEAST(COALESCE(effective_until, $4::date), $4::date)
					ELSE effective_until
				END,
				updated_by=$3
			WHERE id=$1 AND created_by<>$3 AND (
				($2='accrual_paused' AND status='active') OR
				($2='active' AND status='accrual_paused') OR
				($2='ended' AND status IN ('active','accrual_paused'))
			)`, id, status, actor, untilArg)
		if err != nil {
			return err
		}
		if affected, err := result.RowsAffected(); err != nil {
			return fmt.Errorf("read savings enrollment status update: %w", err)
		} else if affected == 0 {
			return sql.ErrNoRows
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO savings_enrollment_status_history
			(id,enrollment_id,status,effective_from,changed_by)
			VALUES ($1,$2,$3,$4,$5)
			ON CONFLICT (enrollment_id,effective_from) DO UPDATE SET
				status=EXCLUDED.status,changed_by=EXCLUDED.changed_by`, identifiers.NewV7(), id,
			status, effectiveFrom.Format("2006-01-02"), actor)
		return err
	})
	if err != nil {
		return fmt.Errorf("update savings enrollment status: %w", err)
	}
	return nil
}

func (r *interestRepo) ListActiveEnrollments(ctx context.Context, date time.Time) ([]model.SavingsEnrollment, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+savingsEnrollmentColumnsWithAlias("e")+` FROM savings_enrollments e
		JOIN savings_products p ON p.id=e.product_id
		JOIN accounts a ON a.id=e.account_id
		JOIN (
			SELECT h.*, LEAD(h.effective_from) OVER (
				PARTITION BY h.enrollment_id ORDER BY h.effective_from
			) AS next_effective_from
			FROM savings_enrollment_status_history h
		) h ON h.enrollment_id=e.id AND h.status='active'
		WHERE p.status <> 'draft'
		AND a.status='active' AND a.currency=p.currency AND a.type=ANY(p.eligible_account_types)
		AND h.effective_from <= $1::date
		AND (h.next_effective_from IS NULL OR h.next_effective_from > $1::date)
		AND e.effective_from <= $1::date
		AND (e.effective_until IS NULL OR e.effective_until > $1::date)
		ORDER BY e.id`, date.Format("2006-01-02"))
	if err != nil {
		return nil, fmt.Errorf("list active savings enrollments: %w", err)
	}
	defer rows.Close()
	result := make([]model.SavingsEnrollment, 0)
	for rows.Next() {
		var enrollment model.SavingsEnrollment
		if err := rows.Scan(enrollmentScanArgs(&enrollment)...); err != nil {
			return nil, fmt.Errorf("scan savings enrollment: %w", err)
		}
		result = append(result, enrollment)
	}
	return result, rows.Err()
}

func (r *interestRepo) ListEnrollmentAccruals(ctx context.Context, enrollmentID uuid.UUID) ([]model.InterestDailyAccrual, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+dailyAccrualColumns+` FROM interest_daily_accruals
		WHERE enrollment_id=$1 ORDER BY accrual_date DESC`, enrollmentID)
	if err != nil {
		return nil, fmt.Errorf("list enrollment interest accruals: %w", err)
	}
	defer rows.Close()
	result := make([]model.InterestDailyAccrual, 0)
	for rows.Next() {
		var item model.InterestDailyAccrual
		if err := rows.Scan(dailyScanArgs(&item)...); err != nil {
			return nil, fmt.Errorf("scan enrollment interest accrual: %w", err)
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

const interestPeriodColumns = `id, public_id, product_id, currency, period_year, period_month,
period_start_at, period_end_at, accrual_cutoff_at, close_not_before_at, status,
expected_item_count, completed_item_count, blocked_item_count, total_accrued_amount,
total_capitalized_amount, opened_at, closing_started_at, closed_at, failed_at,
last_error_code, created_at, updated_at`

func periodScanArgs(period *model.InterestPeriod) []any {
	return []any{&period.ID, &period.PublicID, &period.ProductID, &period.Currency,
		&period.PeriodYear, &period.PeriodMonth, &period.PeriodStartAt, &period.PeriodEndAt,
		&period.AccrualCutoffAt, &period.CloseNotBeforeAt, &period.Status,
		&period.ExpectedItemCount, &period.CompletedItemCount, &period.BlockedItemCount,
		&period.TotalAccruedAmount, &period.TotalCapitalizedAmount, &period.OpenedAt,
		&period.ClosingStartedAt, &period.ClosedAt, &period.FailedAt, &period.LastErrorCode,
		&period.CreatedAt, &period.UpdatedAt}
}

func (r *interestRepo) EnsurePeriod(ctx context.Context, period model.InterestPeriod) (model.InterestPeriod, error) {
	if period.ID == uuid.Nil {
		period.ID = identifiers.NewV7()
	}
	if period.PublicID == "" {
		period.PublicID = period.ID.String()
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO interest_periods
		(id,public_id,product_id,currency,period_year,period_month,period_start_at,
		 period_end_at,accrual_cutoff_at,close_not_before_at,status)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		ON CONFLICT (product_id,period_year,period_month) DO NOTHING`, period.ID,
		period.PublicID, period.ProductID, period.Currency, period.PeriodYear,
		period.PeriodMonth, period.PeriodStartAt, period.PeriodEndAt,
		period.AccrualCutoffAt, period.CloseNotBeforeAt, period.Status)
	if err != nil {
		return model.InterestPeriod{}, fmt.Errorf("ensure interest period: %w", err)
	}
	var stored model.InterestPeriod
	err = r.db.QueryRowContext(ctx, `SELECT `+interestPeriodColumns+` FROM interest_periods
		WHERE product_id=$1 AND period_year=$2 AND period_month=$3`, period.ProductID,
		period.PeriodYear, period.PeriodMonth).Scan(periodScanArgs(&stored)...)
	if err != nil {
		return model.InterestPeriod{}, fmt.Errorf("read interest period: %w", err)
	}
	return stored, nil
}

func (r *interestRepo) GetPeriod(ctx context.Context, id uuid.UUID) (model.InterestPeriod, error) {
	var period model.InterestPeriod
	err := r.db.QueryRowContext(ctx, `SELECT `+interestPeriodColumns+` FROM interest_periods WHERE id=$1`, id).Scan(periodScanArgs(&period)...)
	if err != nil {
		return model.InterestPeriod{}, fmt.Errorf("get interest period: %w", err)
	}
	return period, nil
}

func (r *interestRepo) IsPreviousPeriodClosed(ctx context.Context, periodID uuid.UUID) (bool, error) {
	var closed bool
	err := r.db.QueryRowContext(ctx, `SELECT COALESCE((
		SELECT previous.status='closed' FROM interest_periods current_period
		LEFT JOIN interest_periods previous ON previous.product_id=current_period.product_id
			AND previous.period_start_at < current_period.period_start_at
		WHERE current_period.id=$1
		ORDER BY previous.period_start_at DESC LIMIT 1
	), true)`, periodID).Scan(&closed)
	if err != nil {
		return false, fmt.Errorf("check previous interest period: %w", err)
	}
	return closed, nil
}

func (r *interestRepo) ListEnrollmentPeriods(ctx context.Context, enrollmentID uuid.UUID) ([]model.InterestPeriod, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+interestPeriodColumns+` FROM interest_periods p
		WHERE p.id IN (SELECT DISTINCT period_id FROM interest_daily_accruals WHERE enrollment_id=$1)
		ORDER BY p.period_start_at DESC`, enrollmentID)
	if err != nil {
		return nil, fmt.Errorf("list enrollment interest periods: %w", err)
	}
	defer rows.Close()
	result := make([]model.InterestPeriod, 0)
	for rows.Next() {
		var period model.InterestPeriod
		if err := rows.Scan(periodScanArgs(&period)...); err != nil {
			return nil, fmt.Errorf("scan enrollment interest period: %w", err)
		}
		result = append(result, period)
	}
	return result, rows.Err()
}

func (r *interestRepo) ListEnrollmentCapitalizations(ctx context.Context, enrollmentID uuid.UUID) ([]model.InterestCapitalizationItem, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+capitalizationColumns+` FROM interest_capitalization_items
		WHERE enrollment_id=$1 ORDER BY period_id`, enrollmentID)
	if err != nil {
		return nil, fmt.Errorf("list enrollment interest capitalization items: %w", err)
	}
	defer rows.Close()
	result := make([]model.InterestCapitalizationItem, 0)
	for rows.Next() {
		var item model.InterestCapitalizationItem
		if err := rows.Scan(capitalizationScanArgs(&item)...); err != nil {
			return nil, fmt.Errorf("scan enrollment interest capitalization item: %w", err)
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (r *interestRepo) RefreshExpectedItemCount(ctx context.Context, periodID uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `UPDATE interest_periods p SET expected_item_count=COALESCE((
		SELECT SUM(GREATEST(0,
			(LEAST(COALESCE(e.effective_until, (p.period_end_at AT TIME ZONE sp.timezone)::date + 1),
					COALESCE(h.next_effective_from, (p.period_end_at AT TIME ZONE sp.timezone)::date + 1),
					(p.period_end_at AT TIME ZONE sp.timezone)::date + 1)
					- GREATEST(e.effective_from, h.effective_from, (p.period_start_at AT TIME ZONE sp.timezone)::date))
		))
		FROM (
			SELECT h.*, LEAD(h.effective_from) OVER (
				PARTITION BY h.enrollment_id ORDER BY h.effective_from
			) AS next_effective_from
			FROM savings_enrollment_status_history h
		) h
		JOIN savings_enrollments e ON e.id=h.enrollment_id
		JOIN savings_products sp ON sp.id=e.product_id
		JOIN accounts a ON a.id=e.account_id
		WHERE e.product_id=p.product_id AND h.status='active'
		  AND sp.status <> 'draft' AND a.currency=sp.currency
		  AND a.type=ANY(sp.eligible_account_types)
		  AND h.effective_from < (p.period_end_at AT TIME ZONE sp.timezone)::date + 1
		  AND (h.next_effective_from IS NULL OR h.next_effective_from > (p.period_start_at AT TIME ZONE sp.timezone)::date)
		  AND e.effective_from < (p.period_end_at AT TIME ZONE sp.timezone)::date + 1
		  AND (e.effective_until IS NULL OR e.effective_until > (p.period_start_at AT TIME ZONE sp.timezone)::date)
	),0) WHERE p.id=$1 AND p.status <> 'closed'`, periodID)
	if err != nil {
		return fmt.Errorf("refresh expected interest items: %w", err)
	}
	return nil
}

func (r *interestRepo) CountEligibleEnrollments(ctx context.Context, periodID uuid.UUID) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx, `SELECT count(DISTINCT e.id) FROM savings_enrollments e
		JOIN interest_periods p ON p.id=$1 AND p.product_id=e.product_id
		JOIN savings_products sp ON sp.id=e.product_id
		JOIN accounts a ON a.id=e.account_id
		JOIN (
			SELECT h.*, LEAD(h.effective_from) OVER (
				PARTITION BY h.enrollment_id ORDER BY h.effective_from
			) AS next_effective_from
			FROM savings_enrollment_status_history h
		) h ON h.enrollment_id=e.id AND h.status='active'
		WHERE sp.status <> 'draft'
		  AND a.currency=sp.currency AND a.type=ANY(sp.eligible_account_types)
		  AND h.effective_from < (p.period_end_at AT TIME ZONE sp.timezone)::date + 1
		  AND (h.next_effective_from IS NULL OR h.next_effective_from > (p.period_start_at AT TIME ZONE sp.timezone)::date)
		  AND e.effective_from < (p.period_end_at AT TIME ZONE sp.timezone)::date + 1
		  AND (e.effective_until IS NULL OR e.effective_until > (p.period_start_at AT TIME ZONE sp.timezone)::date)`, periodID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count eligible interest enrollments: %w", err)
	}
	return count, nil
}

func (r *interestRepo) HasNonActiveCapitalizationAccount(ctx context.Context, periodID uuid.UUID) (bool, error) {
	var blocked bool
	err := r.db.QueryRowContext(ctx, `SELECT EXISTS (
		SELECT 1
		FROM savings_enrollments e
		JOIN interest_periods p ON p.id=$1 AND p.product_id=e.product_id
		JOIN savings_products sp ON sp.id=e.product_id
		JOIN accounts account ON account.id=e.account_id
		JOIN (
			SELECT h.*, LEAD(h.effective_from) OVER (
				PARTITION BY h.enrollment_id ORDER BY h.effective_from
			) AS next_effective_from
			FROM savings_enrollment_status_history h
		) h ON h.enrollment_id=e.id AND h.status='active'
		WHERE sp.status <> 'draft'
		  AND account.currency=sp.currency
		  AND account.type=ANY(sp.eligible_account_types)
		  AND h.effective_from < (p.period_end_at AT TIME ZONE sp.timezone)::date + 1
		  AND (h.next_effective_from IS NULL OR h.next_effective_from > (p.period_start_at AT TIME ZONE sp.timezone)::date)
		  AND e.effective_from < (p.period_end_at AT TIME ZONE sp.timezone)::date + 1
		  AND (e.effective_until IS NULL OR e.effective_until > (p.period_start_at AT TIME ZONE sp.timezone)::date)
		  AND account.status <> 'active'
	)`, periodID).Scan(&blocked)
	if err != nil {
		return false, fmt.Errorf("check interest capitalization account status: %w", err)
	}
	return blocked, nil
}

func (r *interestRepo) ListDuePeriods(ctx context.Context, now time.Time) ([]model.InterestPeriod, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+interestPeriodColumns+` FROM interest_periods
		WHERE status IN ('open','failed','closing') AND close_not_before_at <= $1
		ORDER BY period_start_at`, now)
	if err != nil {
		return nil, fmt.Errorf("list due interest periods: %w", err)
	}
	defer rows.Close()
	result := make([]model.InterestPeriod, 0)
	for rows.Next() {
		var period model.InterestPeriod
		if err := rows.Scan(periodScanArgs(&period)...); err != nil {
			return nil, fmt.Errorf("scan due interest period: %w", err)
		}
		result = append(result, period)
	}
	return result, rows.Err()
}

func (r *interestRepo) MarkPeriodStatus(ctx context.Context, tx *sql.Tx, id uuid.UUID, status, code string) error {
	result, err := tx.ExecContext(ctx, `UPDATE interest_periods SET status=$2,
		last_error_code=NULLIF($3,''),
		opened_at=CASE WHEN $2='open' AND opened_at IS NULL THEN now() ELSE opened_at END,
		closing_started_at=CASE WHEN $2='closing' AND closing_started_at IS NULL THEN now() ELSE closing_started_at END,
		closed_at=CASE WHEN $2='closed' THEN COALESCE(closed_at,now()) ELSE closed_at END,
		failed_at=CASE WHEN $2='failed' THEN now() ELSE failed_at END
		WHERE id=$1 AND (
			($2='closing' AND status IN ('open','failed','closing')) OR
			($2='failed' AND status IN ('open','failed','closing')) OR
			($2='closed' AND status='closing')
		)`, id, status, code)
	if err != nil {
		return fmt.Errorf("mark interest period status: %w", err)
	}
	return requireRows(result, "mark interest period status")
}

const dailyAccrualColumns = `id, period_id, enrollment_id, account_id, accrual_date,
snapshot_id, closing_balance, rate_version_id, annual_rate_bps, exact_numerator,
denominator, opening_carry_numerator, recognized_amount, closing_carry_numerator,
status, attempt_count, next_attempt_at, lease_owner, lease_expires_at,
ledger_transaction_id, error_code, created_at, updated_at`

func dailyScanArgs(item *model.InterestDailyAccrual) []any {
	return []any{&item.ID, &item.PeriodID, &item.EnrollmentID, &item.AccountID,
		&item.AccrualDate, &item.SnapshotID, &item.ClosingBalance, &item.RateVersionID,
		&item.AnnualRateBps, &item.ExactNumerator, &item.Denominator,
		&item.OpeningCarryNumerator, &item.RecognizedAmount, &item.ClosingCarryNumerator,
		&item.Status, &item.AttemptCount, &item.NextAttemptAt, &item.LeaseOwner,
		&item.LeaseExpiresAt, &item.LedgerTransactionID, &item.ErrorCode,
		&item.CreatedAt, &item.UpdatedAt}
}

func (r *interestRepo) CreateOrGetDailyAccrual(ctx context.Context, item model.InterestDailyAccrual) (model.InterestDailyAccrual, error) {
	if item.ID == uuid.Nil {
		item.ID = identifiers.NewV7()
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO interest_daily_accruals
		(id,period_id,enrollment_id,account_id,accrual_date,status)
		SELECT $1,$2,$3,$4,$5,'pending' FROM interest_periods
		WHERE id=$2 AND status IN ('open','failed')
		ON CONFLICT (enrollment_id,accrual_date) DO NOTHING`, item.ID, item.PeriodID,
		item.EnrollmentID, item.AccountID, item.AccrualDate.Format("2006-01-02"))
	if err != nil {
		return model.InterestDailyAccrual{}, fmt.Errorf("create daily interest accrual: %w", err)
	}
	var stored model.InterestDailyAccrual
	err = r.db.QueryRowContext(ctx, `SELECT `+dailyAccrualColumns+` FROM interest_daily_accruals WHERE enrollment_id=$1 AND accrual_date=$2`, item.EnrollmentID, item.AccrualDate.Format("2006-01-02")).Scan(dailyScanArgs(&stored)...)
	if err != nil {
		return model.InterestDailyAccrual{}, fmt.Errorf("read daily interest accrual: %w", err)
	}
	return stored, nil
}

func (r *interestRepo) ClaimDailyAccrual(ctx context.Context, owner string, now time.Time) (model.InterestDailyAccrual, error) {
	var item model.InterestDailyAccrual
	err := r.db.QueryRowContext(ctx, `WITH candidate AS (
		SELECT id FROM interest_daily_accruals
		WHERE ((status IN ('pending','retry_wait') AND (next_attempt_at IS NULL OR next_attempt_at <= $2))
		   OR (status='processing' AND lease_expires_at < $2))
		  AND period_id IN (SELECT id FROM interest_periods WHERE status IN ('open','failed'))
		ORDER BY accrual_date, id FOR UPDATE SKIP LOCKED LIMIT 1
	), claimed AS (
		UPDATE interest_daily_accruals a SET status='processing', attempt_count=a.attempt_count+1,
		lease_owner=$1, lease_expires_at=$2 + interval '2 minutes'
		FROM candidate c WHERE a.id=c.id RETURNING a.*
	)
	SELECT `+dailyAccrualColumns+` FROM claimed`, owner, now).Scan(dailyScanArgs(&item)...)
	if err != nil {
		return model.InterestDailyAccrual{}, err
	}
	return item, nil
}

func (r *interestRepo) ClaimDailyAccrualForID(ctx context.Context, id uuid.UUID, owner string, now time.Time) (model.InterestDailyAccrual, error) {
	var item model.InterestDailyAccrual
	err := r.db.QueryRowContext(ctx, `UPDATE interest_daily_accruals SET status='processing',
		attempt_count=attempt_count+1, lease_owner=$2, lease_expires_at=$3 + interval '2 minutes'
		WHERE id=$1 AND ((status IN ('pending','retry_wait') AND (next_attempt_at IS NULL OR next_attempt_at <= $3))
		 OR (status='processing' AND lease_expires_at < $3))
		 AND period_id IN (SELECT id FROM interest_periods WHERE status IN ('open','failed'))
		RETURNING `+dailyAccrualColumns, id, owner, now).Scan(dailyScanArgs(&item)...)
	if err != nil {
		return model.InterestDailyAccrual{}, err
	}
	return item, nil
}

// GetCarryBeforeDate returns the carry produced by the immediately preceding
// terminal accrual.  Using the enrollment's live carry for a historical retry
// could apply a later day's carry to an earlier day; the accrual chain itself
// is the authoritative source for backfill/recovery.
func (r *interestRepo) GetCarryBeforeDate(ctx context.Context, enrollmentID uuid.UUID, date time.Time) (carry, denominator string, found bool, err error) {
	var status string
	var carryValue, denominatorValue sql.NullString
	previousDate := date.AddDate(0, 0, -1).Format("2006-01-02")
	err = r.db.QueryRowContext(ctx, `SELECT closing_carry_numerator, denominator, status
		FROM interest_daily_accruals
		WHERE enrollment_id=$1 AND accrual_date=$2::date`, enrollmentID, previousDate).
		Scan(&carryValue, &denominatorValue, &status)
	if err == nil {
		return terminalCarry(carryValue, denominatorValue, status)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", "", false, fmt.Errorf("get prior interest carry: %w", err)
	}

	// A pause is an intentional accrual gap.  It must preserve the last carry,
	// while an unexpected gap during an active interval remains a readiness
	// failure rather than silently restarting the fraction chain.
	var statusAtPreviousDate string
	historyErr := r.db.QueryRowContext(ctx, `SELECT status
		FROM savings_enrollment_status_history
		WHERE enrollment_id=$1 AND effective_from <= $2::date
		ORDER BY effective_from DESC, id DESC LIMIT 1`, enrollmentID, previousDate).Scan(&statusAtPreviousDate)
	if historyErr != nil && !errors.Is(historyErr, sql.ErrNoRows) {
		return "", "", false, fmt.Errorf("read enrollment status before interest date: %w", historyErr)
	}
	if errors.Is(historyErr, sql.ErrNoRows) {
		if fallbackErr := r.db.QueryRowContext(ctx, `SELECT status FROM savings_enrollments WHERE id=$1`, enrollmentID).Scan(&statusAtPreviousDate); fallbackErr != nil && !errors.Is(fallbackErr, sql.ErrNoRows) {
			return "", "", false, fmt.Errorf("read enrollment status before interest date: %w", fallbackErr)
		}
	}
	var hasEarlier bool
	if earlierErr := r.db.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM interest_daily_accruals WHERE enrollment_id=$1 AND accrual_date < $2::date
	)`, enrollmentID, previousDate).Scan(&hasEarlier); earlierErr != nil {
		return "", "", false, fmt.Errorf("check prior interest accrual gap: %w", earlierErr)
	}
	if statusAtPreviousDate == model.SavingsEnrollmentActive && hasEarlier {
		return "", "", false, fmt.Errorf("prior interest accrual date %s is missing", previousDate)
	}

	err = r.db.QueryRowContext(ctx, `SELECT closing_carry_numerator, denominator, status
		FROM interest_daily_accruals
		WHERE enrollment_id=$1 AND accrual_date < $2::date
		ORDER BY accrual_date DESC LIMIT 1`, enrollmentID, date.Format("2006-01-02")).
		Scan(&carryValue, &denominatorValue, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, fmt.Errorf("get prior interest carry across lifecycle gap: %w", err)
	}
	return terminalCarry(carryValue, denominatorValue, status)
}

func terminalCarry(carryValue, denominatorValue sql.NullString, status string) (string, string, bool, error) {
	if status != model.InterestAccrualCompletedZero && status != model.InterestAccrualCompletedPosted && status != model.InterestAccrualAdjusted {
		return "", "", false, fmt.Errorf("prior interest accrual is not terminal: %s", status)
	}
	if !carryValue.Valid || !denominatorValue.Valid {
		return "", "", false, fmt.Errorf("prior interest accrual has incomplete carry")
	}
	return carryValue.String, denominatorValue.String, true, nil
}

func (r *interestRepo) SaveDailyCalculation(ctx context.Context, tx *sql.Tx, item model.InterestDailyAccrual) error {
	_, err := tx.ExecContext(ctx, `UPDATE interest_daily_accruals SET
		snapshot_id=$2, closing_balance=$3, rate_version_id=$4, annual_rate_bps=$5,
		exact_numerator=$6, denominator=$7, opening_carry_numerator=$8,
		recognized_amount=$9, closing_carry_numerator=$10, error_code=NULL
		WHERE id=$1 AND status='processing'`, item.ID, item.SnapshotID, item.ClosingBalance,
		item.RateVersionID, item.AnnualRateBps, item.ExactNumerator, item.Denominator,
		item.OpeningCarryNumerator, item.RecognizedAmount, item.ClosingCarryNumerator)
	if err != nil {
		return fmt.Errorf("save daily interest calculation: %w", err)
	}
	return nil
}

func (r *interestRepo) CompleteDailyAccrual(ctx context.Context, tx *sql.Tx, id uuid.UUID, status string, transactionID *uuid.UUID, carryNumerator, carryDenominator string) error {
	var enrollmentID uuid.UUID
	var amount sql.NullInt64
	var currentStatus string
	if err := tx.QueryRowContext(ctx, `SELECT enrollment_id, recognized_amount, status FROM interest_daily_accruals WHERE id=$1 FOR UPDATE`, id).Scan(&enrollmentID, &amount, &currentStatus); err != nil {
		return fmt.Errorf("load daily accrual completion: %w", err)
	}
	if currentStatus == model.InterestAccrualCompletedZero || currentStatus == model.InterestAccrualCompletedPosted || currentStatus == model.InterestAccrualAdjusted {
		return nil
	}
	if currentStatus != model.InterestAccrualProcessing {
		return fmt.Errorf("complete daily interest accrual: row %s is %s", id, currentStatus)
	}
	if status == model.InterestAccrualCompletedPosted && amount.Valid && amount.Int64 > 0 && transactionID == nil {
		return fmt.Errorf("complete daily interest accrual: posted row %s requires a ledger transaction", id)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE savings_enrollments SET carry_numerator=$2, carry_denominator=$3, version=version+1 WHERE id=$1`, enrollmentID, carryNumerator, carryDenominator); err != nil {
		return fmt.Errorf("update interest carry: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE interest_daily_accruals SET status=$2, ledger_transaction_id=$3, lease_owner=NULL, lease_expires_at=NULL, next_attempt_at=NULL WHERE id=$1`, id, status, transactionID); err != nil {
		return fmt.Errorf("complete daily interest accrual: %w", err)
	}
	if amount.Valid && amount.Int64 > 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE interest_periods SET total_accrued_amount=total_accrued_amount+$2 WHERE id=(SELECT period_id FROM interest_daily_accruals WHERE id=$1)`, id, amount.Int64); err != nil {
			return fmt.Errorf("update interest period accrued total: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE interest_periods SET completed_item_count=completed_item_count+1 WHERE id=(SELECT period_id FROM interest_daily_accruals WHERE id=$1)`, id); err != nil {
		return fmt.Errorf("update interest period completed count: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE interest_periods SET blocked_item_count=(
		SELECT count(*) FROM interest_daily_accruals WHERE period_id=interest_periods.id AND status IN ('blocked','failed')
	) WHERE id=(SELECT period_id FROM interest_daily_accruals WHERE id=$1)`, id); err != nil {
		return fmt.Errorf("update interest period blocked count: %w", err)
	}
	return nil
}

func (r *interestRepo) FailDailyAccrual(ctx context.Context, id uuid.UUID, status, errorCode string, nextAttempt time.Time) error {
	var nextArg any = nextAttempt
	if nextAttempt.IsZero() {
		nextArg = nil
	}
	_, err := r.db.ExecContext(ctx, `UPDATE interest_daily_accruals SET status=$2,error_code=$3,next_attempt_at=$4,lease_owner=NULL,lease_expires_at=NULL WHERE id=$1`, id, status, errorCode, nextArg)
	if err != nil {
		return fmt.Errorf("fail daily interest accrual: %w", err)
	}
	if _, err := r.db.ExecContext(ctx, `UPDATE interest_periods SET blocked_item_count=(
		SELECT count(*) FROM interest_daily_accruals WHERE period_id=interest_periods.id AND status IN ('blocked','failed')
	) WHERE id=(SELECT period_id FROM interest_daily_accruals WHERE id=$1)`, id); err != nil {
		return fmt.Errorf("update blocked interest item count: %w", err)
	}
	return nil
}

func (r *interestRepo) RetryDailyAccrual(ctx context.Context, id uuid.UUID) error {
	result, err := r.db.ExecContext(ctx, `UPDATE interest_daily_accruals
		SET status='pending',error_code=NULL,next_attempt_at=NULL,lease_owner=NULL,lease_expires_at=NULL
		WHERE id=$1 AND status IN ('blocked','failed','retry_wait')
		  AND period_id IN (SELECT id FROM interest_periods WHERE status IN ('open','closing','failed'))`, id)
	if err != nil {
		return fmt.Errorf("retry daily interest accrual: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read daily interest retry: %w", err)
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	if _, err := r.db.ExecContext(ctx, `UPDATE interest_periods SET blocked_item_count=(
		SELECT count(*) FROM interest_daily_accruals WHERE period_id=interest_periods.id AND status IN ('blocked','failed')
	) WHERE id=(SELECT period_id FROM interest_daily_accruals WHERE id=$1)`, id); err != nil {
		return fmt.Errorf("update blocked interest item count after retry: %w", err)
	}
	return nil
}

func (r *interestRepo) ListPeriodAccruals(ctx context.Context, periodID uuid.UUID) ([]model.InterestDailyAccrual, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+dailyAccrualColumns+` FROM interest_daily_accruals WHERE period_id=$1 ORDER BY accrual_date,enrollment_id`, periodID)
	if err != nil {
		return nil, fmt.Errorf("list interest accruals: %w", err)
	}
	defer rows.Close()
	result := make([]model.InterestDailyAccrual, 0)
	for rows.Next() {
		var item model.InterestDailyAccrual
		if err := rows.Scan(dailyScanArgs(&item)...); err != nil {
			return nil, fmt.Errorf("scan interest accrual: %w", err)
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

const capitalizationColumns = `id, period_id, enrollment_id, account_id,
capitalization_amount, status, attempt_count, next_attempt_at, lease_owner,
lease_expires_at, ledger_transaction_id, error_code, created_at, updated_at`

func capitalizationScanArgs(item *model.InterestCapitalizationItem) []any {
	return []any{&item.ID, &item.PeriodID, &item.EnrollmentID, &item.AccountID,
		&item.CapitalizationAmount, &item.Status, &item.AttemptCount, &item.NextAttemptAt,
		&item.LeaseOwner, &item.LeaseExpiresAt, &item.LedgerTransactionID, &item.ErrorCode,
		&item.CreatedAt, &item.UpdatedAt}
}

func (r *interestRepo) EnsureCapitalizationItems(ctx context.Context, periodID uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO interest_capitalization_items
		(id,period_id,enrollment_id,account_id,capitalization_amount,status)
		SELECT gen_random_uuid(), $1, e.id, e.account_id,
		COALESCE((SELECT SUM(COALESCE(a.recognized_amount,0)) FROM interest_daily_accruals a WHERE a.period_id=$1 AND a.enrollment_id=e.id),0),
		'pending'
		FROM savings_enrollments e
		JOIN interest_periods p ON p.id=$1 AND p.product_id=e.product_id
		JOIN savings_products sp ON sp.id=e.product_id
		WHERE EXISTS (
			SELECT 1
			FROM (
				SELECT h.*, LEAD(h.effective_from) OVER (
					PARTITION BY h.enrollment_id ORDER BY h.effective_from
				) AS next_effective_from
				FROM savings_enrollment_status_history h
			) h
			WHERE h.enrollment_id=e.id AND h.status='active'
			  AND h.effective_from < (p.period_end_at AT TIME ZONE sp.timezone)::date + 1
			  AND (h.next_effective_from IS NULL OR h.next_effective_from > (p.period_start_at AT TIME ZONE sp.timezone)::date)
		)
		  AND e.effective_from < (p.period_end_at AT TIME ZONE sp.timezone)::date + 1
		  AND (e.effective_until IS NULL OR e.effective_until > (p.period_start_at AT TIME ZONE sp.timezone)::date)
		ON CONFLICT (period_id,enrollment_id) DO NOTHING`, periodID)
	if err != nil {
		return fmt.Errorf("ensure capitalization items: %w", err)
	}
	return nil
}

func (r *interestRepo) ListCapitalizationItems(ctx context.Context, periodID uuid.UUID) ([]model.InterestCapitalizationItem, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+capitalizationColumns+` FROM interest_capitalization_items WHERE period_id=$1 ORDER BY enrollment_id`, periodID)
	if err != nil {
		return nil, fmt.Errorf("list capitalization items: %w", err)
	}
	defer rows.Close()
	result := make([]model.InterestCapitalizationItem, 0)
	for rows.Next() {
		var item model.InterestCapitalizationItem
		if err := rows.Scan(capitalizationScanArgs(&item)...); err != nil {
			return nil, fmt.Errorf("scan capitalization item: %w", err)
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (r *interestRepo) StartCapitalization(ctx context.Context, id uuid.UUID, owner string, now time.Time) (model.InterestCapitalizationItem, error) {
	var item model.InterestCapitalizationItem
	err := r.db.QueryRowContext(ctx, `UPDATE interest_capitalization_items SET status='processing',
		attempt_count=attempt_count+1, lease_owner=$2, lease_expires_at=$3 + interval '2 minutes'
		WHERE id=$1 AND ((status IN ('pending','retry_wait') AND (next_attempt_at IS NULL OR next_attempt_at <= $3))
		 OR (status='processing' AND lease_expires_at < $3))
		RETURNING `+capitalizationColumns, id, owner, now).Scan(capitalizationScanArgs(&item)...)
	if err != nil {
		return model.InterestCapitalizationItem{}, err
	}
	return item, nil
}

func (r *interestRepo) CompleteCapitalization(ctx context.Context, tx *sql.Tx, id uuid.UUID, status string, transactionID *uuid.UUID) error {
	var periodID uuid.UUID
	var amount int64
	var currentStatus string
	if err := tx.QueryRowContext(ctx, `SELECT period_id, capitalization_amount, status FROM interest_capitalization_items WHERE id=$1 FOR UPDATE`, id).Scan(&periodID, &amount, &currentStatus); err != nil {
		return fmt.Errorf("load capitalization completion: %w", err)
	}
	if currentStatus == model.InterestCapitalizationPosted || currentStatus == model.InterestCapitalizationCompletedZero || currentStatus == model.InterestCapitalizationAdjusted {
		return nil
	}
	if currentStatus != model.InterestCapitalizationProcessing {
		return fmt.Errorf("complete capitalization item: row %s is %s", id, currentStatus)
	}
	if status == model.InterestCapitalizationPosted && amount > 0 && transactionID == nil {
		return fmt.Errorf("complete capitalization item: posted row %s requires a ledger transaction", id)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE interest_capitalization_items SET status=$2,ledger_transaction_id=$3,lease_owner=NULL,lease_expires_at=NULL,next_attempt_at=NULL WHERE id=$1`, id, status, transactionID); err != nil {
		return fmt.Errorf("complete capitalization item: %w", err)
	}
	if amount > 0 && status == model.InterestCapitalizationPosted {
		if _, err := tx.ExecContext(ctx, `UPDATE interest_periods SET total_capitalized_amount=total_capitalized_amount+$2 WHERE id=$1`, periodID, amount); err != nil {
			return fmt.Errorf("update capitalized period total: %w", err)
		}
	}
	return nil
}

func (r *interestRepo) FailCapitalization(ctx context.Context, id uuid.UUID, status, errorCode string, nextAttempt time.Time) error {
	var nextArg any = nextAttempt
	if nextAttempt.IsZero() {
		nextArg = nil
	}
	_, err := r.db.ExecContext(ctx, `UPDATE interest_capitalization_items SET status=$2,error_code=$3,next_attempt_at=$4,lease_owner=NULL,lease_expires_at=NULL WHERE id=$1`, id, status, errorCode, nextArg)
	if err != nil {
		return fmt.Errorf("fail capitalization item: %w", err)
	}
	return nil
}

func (r *interestRepo) RetryCapitalization(ctx context.Context, id uuid.UUID) error {
	result, err := r.db.ExecContext(ctx, `UPDATE interest_capitalization_items
		SET status='pending',error_code=NULL,next_attempt_at=NULL,lease_owner=NULL,lease_expires_at=NULL
		WHERE id=$1 AND status IN ('blocked','failed','retry_wait')
		  AND period_id IN (SELECT id FROM interest_periods WHERE status IN ('open','closing','failed'))`, id)
	if err != nil {
		return fmt.Errorf("retry capitalization item: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read capitalization retry: %w", err)
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	if _, err := r.db.ExecContext(ctx, `UPDATE interest_periods SET blocked_item_count=(
		SELECT count(*) FROM interest_daily_accruals WHERE period_id=interest_periods.id AND status IN ('blocked','failed')
	) WHERE id=(SELECT period_id FROM interest_capitalization_items WHERE id=$1)`, id); err != nil {
		return fmt.Errorf("update blocked interest item count after capitalization retry: %w", err)
	}
	return nil
}

func (r *interestRepo) PutPeriodCheck(ctx context.Context, check model.InterestPeriodCheck) error {
	details := check.Details
	if len(details) == 0 {
		details = json.RawMessage(`{}`)
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO interest_period_checks
		(id,period_id,check_name,status,expected_value,actual_value,severity,details,checked_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT (period_id,check_name) DO UPDATE SET status=EXCLUDED.status,
		expected_value=EXCLUDED.expected_value,actual_value=EXCLUDED.actual_value,
		severity=EXCLUDED.severity,details=EXCLUDED.details,checked_at=EXCLUDED.checked_at`,
		check.ID, check.PeriodID, check.CheckName, check.Status, check.ExpectedValue,
		check.ActualValue, check.Severity, details, check.CheckedAt)
	if err != nil {
		return fmt.Errorf("put interest period check: %w", err)
	}
	return nil
}

func (r *interestRepo) CreateAdjustment(ctx context.Context, adjustment model.InterestAdjustment) (model.InterestAdjustment, error) {
	if adjustment.ID == uuid.Nil {
		adjustment.ID = identifiers.NewV7()
	}
	if adjustment.PublicID == "" {
		adjustment.PublicID = adjustment.ID.String()
	}
	if adjustment.Status == "" {
		adjustment.Status = "pending_approval"
	}
	result, err := r.db.ExecContext(ctx, `INSERT INTO interest_adjustments
		(id,public_id,source_period_id,enrollment_id,source_accrual_id,source_capitalization_id,
		 amount,direction,status,reason,created_by)
		SELECT $1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11
		WHERE EXISTS (
			SELECT 1 FROM interest_periods p
			JOIN savings_enrollments e ON e.id=$4 AND e.product_id=p.product_id
			WHERE p.id=$3
		)
		AND (
			($5 IS NOT NULL AND $6 IS NULL AND EXISTS (
				SELECT 1 FROM interest_daily_accruals a
				WHERE a.id=$5 AND a.period_id=$3 AND a.enrollment_id=$4
			)) OR
			($5 IS NULL AND $6 IS NOT NULL AND EXISTS (
				SELECT 1 FROM interest_capitalization_items c
				WHERE c.id=$6 AND c.period_id=$3 AND c.enrollment_id=$4
			))
		)`, adjustment.ID, adjustment.PublicID,
		adjustment.SourcePeriodID, adjustment.EnrollmentID, adjustment.SourceAccrualID,
		adjustment.SourceCapitalizationID, adjustment.Amount, adjustment.Direction,
		adjustment.Status, adjustment.Reason, adjustment.CreatedBy)
	if err != nil {
		return model.InterestAdjustment{}, fmt.Errorf("create interest adjustment: %w", err)
	}
	if err := requireRows(result, "create interest adjustment"); err != nil {
		return model.InterestAdjustment{}, fmt.Errorf("%w: adjustment source is not linked to period and enrollment", apperror.ErrValidation)
	}
	return adjustment, nil
}

const interestAdjustmentColumns = `id, public_id, source_period_id, enrollment_id,
source_accrual_id, source_capitalization_id, amount, direction, status, reason,
created_by, approved_by, ledger_transaction_id, created_at, approved_at, posted_at`

func scanInterestAdjustment(scan func(dest ...any) error) (model.InterestAdjustment, error) {
	var (
		adjustment                                             model.InterestAdjustment
		accrualID, capitalizationID, approvedBy, transactionID sql.NullString
		approvedAt, postedAt                                   sql.NullTime
	)
	if err := scan(&adjustment.ID, &adjustment.PublicID, &adjustment.SourcePeriodID,
		&adjustment.EnrollmentID, &accrualID, &capitalizationID, &adjustment.Amount,
		&adjustment.Direction, &adjustment.Status, &adjustment.Reason, &adjustment.CreatedBy,
		&approvedBy, &transactionID, &adjustment.CreatedAt, &approvedAt, &postedAt); err != nil {
		return model.InterestAdjustment{}, err
	}
	if accrualID.Valid {
		value, err := uuid.Parse(accrualID.String)
		if err != nil {
			return model.InterestAdjustment{}, err
		}
		adjustment.SourceAccrualID = &value
	}
	if capitalizationID.Valid {
		value, err := uuid.Parse(capitalizationID.String)
		if err != nil {
			return model.InterestAdjustment{}, err
		}
		adjustment.SourceCapitalizationID = &value
	}
	if approvedBy.Valid {
		adjustment.ApprovedBy = &approvedBy.String
	}
	if transactionID.Valid {
		value, err := uuid.Parse(transactionID.String)
		if err != nil {
			return model.InterestAdjustment{}, err
		}
		adjustment.LedgerTransactionID = &value
	}
	if approvedAt.Valid {
		adjustment.ApprovedAt = &approvedAt.Time
	}
	if postedAt.Valid {
		adjustment.PostedAt = &postedAt.Time
	}
	return adjustment, nil
}

func (r *interestRepo) GetAdjustment(ctx context.Context, id uuid.UUID) (model.InterestAdjustment, error) {
	item, err := scanInterestAdjustment(r.db.QueryRowContext(ctx, `SELECT `+interestAdjustmentColumns+` FROM interest_adjustments WHERE id=$1`, id).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return model.InterestAdjustment{}, fmt.Errorf("interest adjustment %s: %w", id, sql.ErrNoRows)
	}
	if err != nil {
		return model.InterestAdjustment{}, fmt.Errorf("get interest adjustment: %w", err)
	}
	return item, nil
}

func (r *interestRepo) ApproveAdjustment(ctx context.Context, id uuid.UUID, checker string) error {
	result, err := r.db.ExecContext(ctx, `UPDATE interest_adjustments SET status='approved',approved_by=$2,approved_at=now()
		WHERE id=$1 AND status='pending_approval' AND created_by<>$2`, id, checker)
	if err != nil {
		return fmt.Errorf("approve interest adjustment: %w", err)
	}
	return requireRows(result, "approve interest adjustment")
}

func (r *interestRepo) MarkAdjustmentPosted(ctx context.Context, id uuid.UUID, transactionID *uuid.UUID) error {
	return r.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		return r.MarkAdjustmentPostedTx(ctx, tx, id, transactionID)
	})
}

// MarkAdjustmentPostedTx is the transactional form used by the interest
// service when the correction row and its domain outbox event must commit as
// one unit.  It is intentionally an additional concrete-repository method so
// older test/migration doubles implementing InterestRepository remain
// source-compatible.
func (r *interestRepo) MarkAdjustmentPostedTx(ctx context.Context, tx *sql.Tx, id uuid.UUID, transactionID *uuid.UUID) error {
	if transactionID == nil {
		return fmt.Errorf("mark interest adjustment posted: ledger transaction is required")
	}
	result, err := tx.ExecContext(ctx, `UPDATE interest_adjustments SET status='posted',
		ledger_transaction_id=$2,posted_at=COALESCE(posted_at,now())
		WHERE id=$1 AND (status='approved' OR (status='posted' AND ledger_transaction_id IS NOT DISTINCT FROM $2))`, id, transactionID)
	if err != nil {
		return fmt.Errorf("mark interest adjustment posted: %w", err)
	}
	return requireRows(result, "mark interest adjustment posted")
}

func requireRows(result sql.Result, operation string) error {
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("%s rows affected: %w", operation, err)
	}
	if rows == 0 {
		return fmt.Errorf("%s: no eligible row", operation)
	}
	return nil
}
