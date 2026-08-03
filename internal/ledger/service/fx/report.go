package fx

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/ledger/apperror"
	"github.com/herdifirdausss/seev/internal/ledger/model"
)

const (
	defaultFXDailyReportWindow = 7 * 24 * time.Hour
	maxFXDailyReportWindow     = 366 * 24 * time.Hour
)

// DailyPositionReport returns one row per pair/currency/day. Opening and
// closing balances are derived from the current position account projection
// and posted ledger entries; conversion and governed rebalance flows remain
// separate fields in their native currency minor units.
func (s *Service) DailyPositionReport(ctx context.Context, from, to time.Time) ([]model.FXDailyPosition, error) {
	now := s.now()
	if from.IsZero() {
		from = now.Add(-defaultFXDailyReportWindow)
	}
	if to.IsZero() {
		to = now
	}
	if !to.After(from) {
		return nil, fmt.Errorf("%w: FX daily position report window must end after it starts", apperror.ErrValidation)
	}
	if to.Sub(from) > maxFXDailyReportWindow {
		return nil, fmt.Errorf("%w: FX daily position report window cannot exceed %s", apperror.ErrValidation, maxFXDailyReportWindow)
	}

	rows, err := s.db.QueryContext(ctx, `
		WITH days AS (
			SELECT generate_series(
				date_trunc('day', $1::timestamptz),
				date_trunc('day', ($2::timestamptz - interval '1 microsecond')),
				interval '1 day'
			) AS report_day
		), positions AS (
			SELECT l.pair_id, p.pair_code, l.currency, a.id AS account_id,
			       c.minor_unit, l.minimum_balance, l.maximum_balance,
			       COALESCE(l.warning_minimum_balance, l.minimum_balance) AS warning_minimum_balance,
			       COALESCE(l.warning_maximum_balance, l.maximum_balance) AS warning_maximum_balance,
			       COALESCE(l.critical_minimum_balance, l.minimum_balance) AS critical_minimum_balance,
			       COALESCE(l.critical_maximum_balance, l.maximum_balance) AS critical_maximum_balance,
			       ab.balance
			FROM fx_position_limits l
			JOIN fx_pairs p ON p.id = l.pair_id
			JOIN currencies c ON c.code = l.currency
			LEFT JOIN accounts a
			  ON a.owner_type = 'system' AND a.type = 'fx_conversion'
			 AND a.currency = l.currency AND a.system_qualifier = p.position_qualifier
			 AND a.status = 'active'
			LEFT JOIN account_balances ab ON ab.account_id = a.id
		)
		SELECT d.report_day, p.pair_id, p.pair_code, p.currency, p.account_id,
		       p.minor_unit,
		       COALESCE(p.balance, 0) - COALESCE((
			   SELECT SUM(CASE WHEN le.direction = 'credit' THEN le.amount ELSE -le.amount END)::bigint
			   FROM ledger_entries le
			   JOIN ledger_transactions lt ON lt.id = le.transaction_id
			   WHERE le.account_id = p.account_id AND lt.status = 'posted'
			     AND lt.created_at >= d.report_day
			), 0) AS opening_balance,
		       COALESCE((
			   SELECT SUM(le.amount)::bigint
			   FROM ledger_entries le
			   JOIN ledger_transactions lt ON lt.id = le.transaction_id
			   WHERE le.account_id = p.account_id AND lt.status = 'posted'
			     AND lt.created_at >= d.report_day AND lt.created_at < d.report_day + interval '1 day'
			     AND lt.type = 'fx_out' AND le.direction = 'credit'
		       ), 0) AS conversion_inflow,
		       COALESCE((
			   SELECT SUM(le.amount)::bigint
			   FROM ledger_entries le
			   JOIN ledger_transactions lt ON lt.id = le.transaction_id
			   WHERE le.account_id = p.account_id AND lt.status = 'posted'
			     AND lt.created_at >= d.report_day AND lt.created_at < d.report_day + interval '1 day'
			     AND lt.type = 'fx_in' AND le.direction = 'debit'
		       ), 0) AS conversion_outflow,
		       COALESCE((
			   SELECT SUM(le.amount)::bigint
			   FROM ledger_entries le
			   JOIN ledger_transactions lt ON lt.id = le.transaction_id
			   WHERE le.account_id = p.account_id AND lt.status = 'posted'
			     AND lt.created_at >= d.report_day AND lt.created_at < d.report_day + interval '1 day'
			     AND lt.type = 'fx_rebalance_credit' AND le.direction = 'credit'
		       ), 0) AS rebalance_credit,
		       COALESCE((
			   SELECT SUM(le.amount)::bigint
			   FROM ledger_entries le
			   JOIN ledger_transactions lt ON lt.id = le.transaction_id
			   WHERE le.account_id = p.account_id AND lt.status = 'posted'
			     AND lt.created_at >= d.report_day AND lt.created_at < d.report_day + interval '1 day'
			     AND lt.type = 'fx_rebalance_debit' AND le.direction = 'debit'
		       ), 0) AS rebalance_debit,
		       COALESCE(p.balance, 0) - COALESCE((
			   SELECT SUM(CASE WHEN le.direction = 'credit' THEN le.amount ELSE -le.amount END)::bigint
			   FROM ledger_entries le
			   JOIN ledger_transactions lt ON lt.id = le.transaction_id
			   WHERE le.account_id = p.account_id AND lt.status = 'posted'
			     AND lt.created_at >= d.report_day + interval '1 day'
			), 0) AS closing_balance,
		       p.minimum_balance, p.maximum_balance,
		       p.warning_minimum_balance, p.warning_maximum_balance,
		       p.critical_minimum_balance, p.critical_maximum_balance
		FROM days d
		CROSS JOIN positions p
		ORDER BY d.report_day, p.pair_code, p.currency`, from, to)
	if err != nil {
		return nil, fmt.Errorf("query FX daily position report: %w", err)
	}
	defer rows.Close()

	result := make([]model.FXDailyPosition, 0)
	for rows.Next() {
		var item model.FXDailyPosition
		var pairCode, code string
		var accountID uuid.NullUUID
		var minimum, maximum, warningMinimum, warningMaximum, criticalMinimum, criticalMaximum int64
		if err := rows.Scan(
			&item.Date, &item.PairID, &pairCode, &code, &accountID, &item.MinorUnit,
			&item.OpeningBalance, &item.ConversionInflow, &item.ConversionOutflow,
			&item.RebalanceCredit, &item.RebalanceDebit, &item.ClosingBalance,
			&minimum, &maximum, &warningMinimum, &warningMaximum,
			&criticalMinimum, &criticalMaximum,
		); err != nil {
			return nil, fmt.Errorf("scan FX daily position report: %w", err)
		}
		item.PairCode = strings.TrimSpace(pairCode)
		item.Currency = strings.TrimSpace(code)
		if accountID.Valid {
			item.AccountID = accountID.UUID
			item.State = positionState(item.ClosingBalance, minimum, maximum, warningMinimum, warningMaximum, criticalMinimum, criticalMaximum)
		} else {
			item.State = "critical"
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate FX daily position report: %w", err)
	}
	return result, nil
}
