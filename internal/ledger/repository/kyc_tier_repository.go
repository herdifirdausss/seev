package repository

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/ledger/apperror"
	"github.com/herdifirdausss/seev/pkg/database"
	"github.com/herdifirdausss/seev/pkg/generalutil"
)

// KycTierRepository applies a policy_tier_limits template to one user's
// policy_limits rows (docs/plan/39 Task T5) — invoked when auth-service
// approves a KYC tier upgrade via the gRPC ApplyKycTier RPC. Self-contained
// (owns its own transaction) rather than taking a caller-supplied *sql.Tx,
// same convention as internal/ledger/feepolicy's CreateQuote/ConsumeQuote:
// this is always the entire unit of work for the RPC call, never a step
// inside a larger ledger posting transaction.
type KycTierRepository interface {
	// Apply upserts policy_limits for userID from every policy_tier_limits
	// row matching kycLevel — one row per transaction_type, ON CONFLICT
	// (user_id, transaction_type) DO UPDATE, so re-applying the same level
	// (or a fresh downgrade/upgrade) is idempotent. Returns
	// apperror.ErrUnknownKycTier if kycLevel matches zero template rows.
	Apply(ctx context.Context, userID uuid.UUID, kycLevel int32) error
}

type kycTierRepo struct {
	db database.DatabaseSQL
}

func NewKycTierRepository(db database.DatabaseSQL) KycTierRepository {
	return &kycTierRepo{db: db}
}

type tierLimitTemplate struct {
	transactionType  string
	maxPerTx         sql.NullInt64
	maxDailyAmount   sql.NullInt64
	maxDailyCount    sql.NullInt32
	maxMonthlyAmount sql.NullInt64
}

const selectTierTemplatesQuery = `
	SELECT transaction_type, max_per_tx, max_daily_amount, max_daily_count, max_monthly_amount
	FROM policy_tier_limits
	WHERE kyc_level = $1`

const upsertPolicyLimitQuery = `
	INSERT INTO policy_limits
		(id, user_id, transaction_type, max_per_tx, max_daily_amount, max_daily_count, max_monthly_amount, enabled)
	VALUES ($1,$2,$3,$4,$5,$6,$7,true)
	ON CONFLICT (user_id, transaction_type) DO UPDATE SET
		max_per_tx = EXCLUDED.max_per_tx,
		max_daily_amount = EXCLUDED.max_daily_amount,
		max_daily_count = EXCLUDED.max_daily_count,
		max_monthly_amount = EXCLUDED.max_monthly_amount,
		enabled = true`

func (r *kycTierRepo) Apply(ctx context.Context, userID uuid.UUID, kycLevel int32) error {
	return r.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, selectTierTemplatesQuery, kycLevel)
		if err != nil {
			return fmt.Errorf("query policy tier limits: %w", err)
		}
		var templates []tierLimitTemplate
		for rows.Next() {
			var t tierLimitTemplate
			if scanErr := rows.Scan(&t.transactionType, &t.maxPerTx, &t.maxDailyAmount, &t.maxDailyCount, &t.maxMonthlyAmount); scanErr != nil {
				rows.Close()
				return fmt.Errorf("scan policy tier limit: %w", scanErr)
			}
			templates = append(templates, t)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate policy tier limits: %w", err)
		}
		rows.Close()

		if len(templates) == 0 {
			return apperror.ErrUnknownKycTier
		}

		for _, t := range templates {
			_, err := tx.ExecContext(ctx, upsertPolicyLimitQuery,
				generalutil.NewV7(), userID, t.transactionType,
				t.maxPerTx, t.maxDailyAmount, t.maxDailyCount, t.maxMonthlyAmount,
			)
			if err != nil {
				return fmt.Errorf("upsert policy limit for %s: %w", t.transactionType, err)
			}
		}
		return nil
	})
}
