//go:build integration

// TestInterestPeriodClose_HappyPath is the C5 Part A evidence test
// (docs/roadmap/active/61-c5-advanced-financial-products-period-close.md §3):
// RunDaily → accrual completed_posted → ClosePeriod → period closed →
// second ClosePeriod rejected (ErrClosedPeriodImmutable) →
// DB trigger (fn_prevent_c5_closed_period_mutation) rejects raw UPDATE.
//
// The test runs against a throwaway Postgres container (testcontainers-go)
// with the full real migration set applied so schema + trigger correctness is
// tested alongside service correctness — not just the Go code path.
package ledger_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/internal/platform/database"
	interestservice "github.com/herdifirdausss/seev/services/ledger/internal/ledger/interest"
	"github.com/herdifirdausss/seev/services/ledger/internal/ledger/model"
	"github.com/herdifirdausss/seev/services/ledger/internal/ledger/provision"
	"github.com/herdifirdausss/seev/services/ledger/internal/repository"
)

// txLookupAdapter bridges repository.TransactionRepository →
// interestservice.TransactionLookup without requiring a full ledger.Module.
type txLookupAdapter struct {
	repo repository.TransactionRepository
}

func (a *txLookupAdapter) GetTransactionByIdempotencyKey(ctx context.Context, key, scope string) (model.LedgerTransaction, error) {
	var scopePtr *string
	if scope != "" {
		scopePtr = &scope
	}
	return a.repo.GetByIdempotencyKey(ctx, key, scopePtr)
}

// newInterestService wires interest.Service against real repositories.
// It follows the same pattern as newService/newScheduleService in this file
// and wires the txLookup adapter so accrual completion can link the ledger
// transaction ID (required for completed_posted status).
func newInterestService(db *database.DBSQL) *interestservice.Service {
	handleSvc, _ := newService(db)
	interestRepo := repository.NewInterestRepository(db)
	snapshotRepo := repository.NewSnapshotRepository(db, testSnapshotLoc())
	txRepo := repository.NewTransactionRepository(db, schemaTestDigestRing())
	svc := interestservice.New(db, interestRepo, snapshotRepo, handleSvc, slog.Default(), testSnapshotLoc())
	svc.SetTransactionLookup(&txLookupAdapter{repo: txRepo})
	return svc
}

// system account IDs seeded by migrations 000016 and 000040.
const (
	interestExpenseIDR = "00000000-0000-0000-0000-000000000029"
	interestPayableIDR = "00000000-0000-0000-0000-000000000031"
)

// seedInterestScenario provisions the minimum DB state needed for a single-
// enrollment RunDaily + ClosePeriod end-to-end pass:
//
//	product (IDR, active) → rate (500 bps, effective 2025-01-01) →
//	enrollment (active, 2026-01-01) → balance snapshot for 2026-01-31
//
// Uses distinct maker ("alice") and checker ("bob") everywhere the maker-
// checker guard applies (UpdateProductStatus, ApproveRate).
func seedInterestScenario(
	t *testing.T,
	db *database.DBSQL,
	interestRepo repository.InterestRepository,
	userID, cashAccountID uuid.UUID,
) model.SavingsEnrollment {
	t.Helper()
	ctx := context.Background()

	product, err := interestRepo.CreateProduct(ctx, model.SavingsProduct{
		ProductCode:              "SEEV-TEST-BASIC",
		Name:                     "Test Basic Savings",
		Currency:                 "IDR",
		InterestExpenseAccountID: uuid.MustParse(interestExpenseIDR),
		InterestPayableAccountID: uuid.MustParse(interestPayableIDR),
		CreatedBy:                "alice",
		UpdatedBy:                "alice",
	})
	require.NoError(t, err)

	require.NoError(t, interestRepo.UpdateProductStatus(ctx, product.ID, model.SavingsProductActive, "bob"))

	rate, err := interestRepo.CreateRate(ctx, model.SavingsRateVersion{
		ProductID:     product.ID,
		AnnualRateBps: 500,
		EffectiveFrom: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		CreatedBy:     "alice",
	})
	require.NoError(t, err)

	require.NoError(t, interestRepo.SubmitRate(ctx, rate.ID, "alice"))
	require.NoError(t, interestRepo.ApproveRate(ctx, rate.ID, "bob"))

	enrollment, err := interestRepo.CreateEnrollment(ctx, model.SavingsEnrollment{
		ProductID:     product.ID,
		AccountID:     cashAccountID,
		UserID:        userID,
		Status:        model.SavingsEnrollmentActive,
		EffectiveFrom: time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC),
		CreatedBy:     "alice",
		UpdatedBy:     "alice",
	})
	require.NoError(t, err)

	// Seed a balance snapshot for the accrual date (2026-01-31) via direct
	// SQL: InsertForDate computes from live ledger entries, which don't exist
	// for a synthetic past date in a clean test container.
	_, err = db.ExecContext(ctx,
		`INSERT INTO account_balance_snapshots (account_id, as_of_date, closing_balance, entry_count)
		 VALUES ($1, '2026-01-31', 1000000, 1)`,
		cashAccountID)
	require.NoError(t, err)

	return enrollment
}

// TestInterestPeriodClose_HappyPath drives the full C5 Part A scenario
// against a real Postgres container with all migrations applied.
func TestInterestPeriodClose_HappyPath(t *testing.T) {
	db := setupSchemaTestDB(t)
	ctx := context.Background()

	interestRepo := repository.NewInterestRepository(db)
	interestSvc := newInterestService(db)

	// Provision a user + standard account set (cash, hold, pending, frozen).
	userID := uuid.New()
	_, err := provision.New(db, repository.NewProvisioningRepository()).CreateUserAccounts(ctx, userID, "IDR")
	require.NoError(t, err)

	accRepo := repository.NewAccountRepository(db)
	cashID, err := accRepo.GetAccountID(ctx, userID, "cash")
	require.NoError(t, err)

	// Seed product → rate → enrollment → snapshot.
	enrollment := seedInterestScenario(t, db, interestRepo, userID, cashID)

	// Jan 31, 2026 at midnight Asia/Jakarta — the accrual date.
	jan31 := time.Date(2026, 1, 31, 0, 0, 0, 0, testSnapshotLoc())

	// RunDaily should process the enrollment and produce a completed_posted
	// accrual (balance=1,000,000; rate=500 bps; recognized=136 IDR from the
	// ACT/365F math: floor(1_000_000*500/3_650_000) = 136).
	interestSvc.RunDaily(ctx, jan31)

	accruals, err := interestRepo.ListEnrollmentAccruals(ctx, enrollment.ID)
	require.NoError(t, err)
	require.Len(t, accruals, 1, "RunDaily must have created exactly one accrual row for jan31")

	accrual := accruals[0]
	require.Equal(t, model.InterestAccrualCompletedPosted, accrual.Status,
		"accrual must be completed_posted when snapshot balance > 0 and rate > 0")
	require.NotNil(t, accrual.LedgerTransactionID,
		"completed_posted accrual must carry a linked ledger transaction ID")

	// Retrieve the period that ensurePeriodForDate created.
	periods, err := interestRepo.ListEnrollmentPeriods(ctx, enrollment.ID)
	require.NoError(t, err)
	require.Len(t, periods, 1)
	periodID := periods[0].ID

	// ClosePeriod must succeed: all accruals are terminal and the January 2026
	// close_not_before_at (2026-02-01 01:15 WIB) is safely in the past.
	require.NoError(t, interestSvc.ClosePeriod(ctx, periodID, "closer"),
		"ClosePeriod must succeed when all accruals are terminal")

	period, err := interestRepo.GetPeriod(ctx, periodID)
	require.NoError(t, err)
	assert.Equal(t, model.InterestPeriodClosed, period.Status,
		"period must be closed after a successful ClosePeriod")
	assert.Equal(t, period.TotalAccruedAmount, period.TotalCapitalizedAmount,
		"total_accrued_amount must equal total_capitalized_amount after period close")

	// A second ClosePeriod on the same (already closed) period must be
	// rejected with ErrClosedPeriodImmutable — not panic, not a DB error.
	err = interestSvc.ClosePeriod(ctx, periodID, "closer")
	require.Error(t, err)
	assert.True(t, errors.Is(err, interestservice.ErrClosedPeriodImmutable),
		"second ClosePeriod must return ErrClosedPeriodImmutable, got: %v", err)

	// DB-level immutability: the fn_prevent_c5_closed_period_mutation trigger
	// (migration 000040) must reject any raw UPDATE to a closed period row,
	// even bypassing the Go service layer entirely.
	_, triggerErr := db.ExecContext(ctx,
		`UPDATE interest_periods SET status='open' WHERE id=$1`, periodID)
	require.Error(t, triggerErr,
		"fn_prevent_c5_closed_period_mutation trigger must reject raw UPDATE on a closed period")
}
