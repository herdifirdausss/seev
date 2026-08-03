package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/herdifirdausss/seev/internal/payin/model"
	"github.com/herdifirdausss/seev/pkg/generalutil"
)

func (r *repo) InsertTopupIntent(ctx context.Context, intent model.TopupIntent) error {
	intent.NormalizeFinancials()
	amount, err := minorUnits(intent.Amount)
	if err != nil {
		return err
	}
	fee, err := minorUnits(intent.FeeAmount)
	if err != nil {
		return err
	}
	total, err := minorUnits(intent.TotalDebit)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `
		INSERT INTO payin_topup_intents
			(id, reference, user_id, merchant_tenant_id, amount, currency, vendor, gateway, status,
			 expires_at, request_id, fee_quote_id, fee_rule_id, fee_gateway, fee_amount,
			 total_debit, fee_application, fee_quote_consumed_at, fee_snapshot_version,
			 created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'pending', $9, $10, $11, $12, $13,
			$14, $15, $16, $17, $18, now(), now())`,
		intent.ID, intent.Reference, intent.UserID, intent.MerchantTenantID, amount, intent.Currency, intent.Vendor, intent.Gateway, intent.ExpiresAt,
		generalutil.NullString(intent.RequestID), intent.FeeQuoteID, intent.FeeRuleID, generalutil.NullString(intent.FeeGateway), fee,
		total, intent.FeeApplication, intent.FeeQuoteConsumedAt, intent.FeeSnapshotVersion,
	)
	if err != nil {
		return fmt.Errorf("insert payin topup intent: %w", err)
	}
	return nil
}

// UpdateTopupFeeSnapshot completes the durable quote-consumption handshake.
// The intent is inserted first with the quote id and a fee-free provisional
// total; this update makes a quote-consumed snapshot visible after either side
// crashes. Repeating the exact update is intentionally idempotent.
func (r *repo) UpdateTopupFeeSnapshot(ctx context.Context, id, quoteID uuid.UUID, fee, totalDebit decimal.Decimal, feeGateway string, consumedAt time.Time) error {
	feeMinor, err := minorUnits(fee)
	if err != nil { return err }
	totalMinor, err := minorUnits(totalDebit)
	if err != nil { return err }
	result, err := r.db.ExecContext(ctx, `UPDATE payin_topup_intents SET
		fee_amount=$3,total_debit=$4,fee_gateway=NULLIF($5,''),
		fee_quote_consumed_at=COALESCE(fee_quote_consumed_at,$6),fee_snapshot_version=1,updated_at=now()
		WHERE id=$1 AND fee_quote_id=$2 AND status='pending'
		  AND (fee_quote_consumed_at IS NULL OR
		       (fee_amount=$3 AND total_debit=$4 AND COALESCE(fee_gateway,'')=COALESCE($5,'')))`,
		id, quoteID, feeMinor, totalMinor, feeGateway, consumedAt)
	if err != nil {
		return fmt.Errorf("update payin topup fee snapshot: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil { return fmt.Errorf("update payin topup fee snapshot rows affected: %w", err) }
	if rows == 0 { return fmt.Errorf("update payin topup fee snapshot: intent is not pending or quote does not match") }
	return nil
}

func (r *repo) InsertMerchantTopupIntent(ctx context.Context, intent model.TopupIntent) (model.TopupIntent, error) {
	if intent.DownstreamKey == "" {
		return model.TopupIntent{}, fmt.Errorf("insert merchant payin topup intent: downstream key is required")
	}
	intent.NormalizeFinancials()
	amount, err := minorUnits(intent.Amount)
	if err != nil {
		return model.TopupIntent{}, err
	}
	fee, err := minorUnits(intent.FeeAmount)
	if err != nil {
		return model.TopupIntent{}, err
	}
	total, err := minorUnits(intent.TotalDebit)
	if err != nil {
		return model.TopupIntent{}, err
	}
	_, err = r.db.ExecContext(ctx, `
		INSERT INTO payin_topup_intents
			(id, reference, user_id, merchant_tenant_id, amount, currency, vendor, gateway, status,
			 expires_at, request_id, downstream_key, fee_quote_id, fee_rule_id, fee_gateway,
			 fee_amount, total_debit, fee_application, fee_quote_consumed_at,
			 fee_snapshot_version, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'pending', $9, $10, $11, $12, $13, $14,
			$15, $16, $17, $18, $19, now(), now())
		ON CONFLICT (merchant_tenant_id, downstream_key) WHERE downstream_key IS NOT NULL DO NOTHING`,
		intent.ID, intent.Reference, intent.UserID, intent.MerchantTenantID, amount, intent.Currency, intent.Vendor, intent.Gateway, intent.ExpiresAt,
		generalutil.NullString(intent.RequestID), intent.DownstreamKey, intent.FeeQuoteID, intent.FeeRuleID,
		generalutil.NullString(intent.FeeGateway), fee, total, intent.FeeApplication,
		intent.FeeQuoteConsumedAt, intent.FeeSnapshotVersion,
	)
	if err != nil {
		return model.TopupIntent{}, fmt.Errorf("insert merchant payin topup intent: %w", err)
	}
	row := r.db.QueryRowContext(ctx, `
		SELECT `+topupIntentColumns+`
		FROM payin_topup_intents WHERE merchant_tenant_id = $1 AND downstream_key = $2`, intent.MerchantTenantID, intent.DownstreamKey)
	stored, err := scanTopupIntent(row)
	if err != nil {
		return model.TopupIntent{}, fmt.Errorf("read merchant payin topup intent after insert: %w", err)
	}
	return stored, nil
}

func (r *repo) GetTopupIntent(ctx context.Context, id uuid.UUID) (model.TopupIntent, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT `+topupIntentColumns+`
		FROM payin_topup_intents WHERE id = $1`, id)
	intent, err := scanTopupIntent(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.TopupIntent{}, ErrNotFound
	}
	return intent, err
}

func (r *repo) GetTopupIntentByReference(ctx context.Context, reference string) (model.TopupIntent, bool, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT `+topupIntentColumns+`
		FROM payin_topup_intents WHERE reference = $1`, reference)
	intent, err := scanTopupIntent(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.TopupIntent{}, false, nil
	}
	if err != nil {
		return model.TopupIntent{}, false, err
	}
	return intent, true, nil
}

func (r *repo) GetTopupIntentByFeeQuoteID(ctx context.Context, quoteID uuid.UUID) (model.TopupIntent, bool, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+topupIntentColumns+`
		FROM payin_topup_intents WHERE fee_quote_id=$1`, quoteID)
	intent, err := scanTopupIntent(row)
	if errors.Is(err, sql.ErrNoRows) { return model.TopupIntent{}, false, nil }
	if err != nil { return model.TopupIntent{}, false, err }
	return intent, true, nil
}

func scanTopupIntent(row *sql.Row) (model.TopupIntent, error) {
	var intent model.TopupIntent
	var amount, feeAmount, totalDebit int64
	var settledEventID, feeQuoteID, feeRuleID sql.NullString
	var feeGateway, feeApplication sql.NullString
	var feeQuoteConsumedAt sql.NullTime
	if err := row.Scan(&intent.ID, &intent.Reference, &intent.UserID, &intent.MerchantTenantID, &amount,
		&intent.Currency, &intent.Vendor, &intent.Gateway, &intent.Status, &settledEventID, &intent.ExpiresAt,
		&intent.RequestID, &intent.DownstreamKey, &feeQuoteID, &feeRuleID, &feeGateway,
		&feeAmount, &totalDebit, &feeApplication, &feeQuoteConsumedAt,
		&intent.FeeSnapshotVersion, &intent.CreatedAt, &intent.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.TopupIntent{}, err
		}
		return model.TopupIntent{}, fmt.Errorf("scan payin topup intent: %w", err)
	}
	intent.Amount = decimal.NewFromInt(amount)
	intent.FeeAmount = decimal.NewFromInt(feeAmount)
	intent.TotalDebit = decimal.NewFromInt(totalDebit)
	if feeGateway.Valid {
		intent.FeeGateway = feeGateway.String
	}
	if feeApplication.Valid {
		intent.FeeApplication = feeApplication.String
	}
	if feeQuoteConsumedAt.Valid {
		intent.FeeQuoteConsumedAt = &feeQuoteConsumedAt.Time
	}
	if feeQuoteID.Valid {
		id, err := uuid.Parse(feeQuoteID.String)
		if err != nil {
			return model.TopupIntent{}, fmt.Errorf("parse fee_quote_id: %w", err)
		}
		intent.FeeQuoteID = &id
	}
	if feeRuleID.Valid {
		id, err := uuid.Parse(feeRuleID.String)
		if err != nil {
			return model.TopupIntent{}, fmt.Errorf("parse fee_rule_id: %w", err)
		}
		intent.FeeRuleID = &id
	}
	if settledEventID.Valid {
		id, err := uuid.Parse(settledEventID.String)
		if err != nil {
			return model.TopupIntent{}, fmt.Errorf("parse settled_event_id: %w", err)
		}
		intent.SettledEventID = &id
	}
	intent.NormalizeFinancials()
	return intent, nil
}

const topupIntentColumns = `id, reference, user_id, merchant_tenant_id, amount, currency,
vendor, COALESCE(gateway, ''), status, settled_event_id, expires_at, COALESCE(request_id, ''),
COALESCE(downstream_key, ''), fee_quote_id, fee_rule_id, COALESCE(fee_gateway, ''),
fee_amount, total_debit, COALESCE(fee_application, 'added_on_top'),
fee_quote_consumed_at, fee_snapshot_version, created_at, updated_at`

func minorUnits(value decimal.Decimal) (int64, error) {
	if value.IsNegative() || !value.Equal(value.Truncate(0)) || !value.BigInt().IsInt64() {
		return 0, fmt.Errorf("payin: money amount must be a non-negative integer within int64")
	}
	return value.IntPart(), nil
}

func (r *repo) MarkTopupIntentSettled(ctx context.Context, reference string, eventID uuid.UUID) (bool, error) {
	result, err := r.db.ExecContext(ctx, `
		UPDATE payin_topup_intents SET status = 'settled', settled_event_id = $1, updated_at = now()
		WHERE reference = $2 AND status = 'pending'`,
		eventID, reference)
	if err != nil {
		return false, fmt.Errorf("mark payin topup intent settled: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("mark payin topup intent settled: rows affected: %w", err)
	}
	return n > 0, nil
}

func (r *repo) MarkTopupIntentExpired(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE payin_topup_intents SET status = 'expired', updated_at = now()
		WHERE id = $1 AND status = 'pending'`, id)
	if err != nil {
		return fmt.Errorf("mark payin topup intent expired: %w", err)
	}
	return nil
}
