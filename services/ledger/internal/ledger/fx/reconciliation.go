package fx

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/services/ledger/internal/ledger/errors"
	"github.com/herdifirdausss/seev/services/ledger/internal/ledger/model"
)

const (
	defaultFXReconciliationLimit = 100
	maxFXReconciliationLimit     = 1000
)

// ReconcileConversions checks the durable evidence for conversions in a
// bounded time window. It is intentionally read-only: a discrepancy is an
// operator/Assurance finding, never a reason to mutate balances or silently
// synthesize a missing FX leg.
func (s *Service) ReconcileConversions(ctx context.Context, from, to time.Time, limit int) (model.FXReconciliationReport, error) {
	now := s.now()
	if from.IsZero() {
		from = now.Add(-24 * time.Hour)
	}
	if to.IsZero() {
		to = now
	}
	if !to.After(from) {
		return model.FXReconciliationReport{}, fmt.Errorf("%w: reconciliation window must have an end after its start", apperror.ErrValidation)
	}
	if limit <= 0 || limit > maxFXReconciliationLimit {
		limit = defaultFXReconciliationLimit
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT c.id, c.quote_id, c.source_currency, c.target_currency,
		       c.source_amount, c.target_amount, c.status,
		       c.source_transaction_id, c.target_transaction_id,
		       COALESCE(st.status, ''), COALESCE(tt.status, ''),
		       COALESCE(c.error_message, ''),
		       COALESCE(
		           q.id IS NOT NULL
		           AND q.user_id = c.user_id
		           AND q.status = 'consumed'
		           AND q.consumed_by_conversion_id = c.id
		           AND q.source_currency = c.source_currency
		           AND q.target_currency = c.target_currency
		           AND q.source_amount = c.source_amount
		           AND q.target_amount = c.target_amount,
		           false
		       ),
		       COALESCE(
		           st.id IS NOT NULL
		           AND st.conversion_id = c.id
		           AND st.fx_quote_id = c.quote_id
		           AND st.fx_leg = 'source'
		           AND st.counterpart_transaction_id = c.target_transaction_id
		           AND st.type = 'fx_out'
		           AND st.currency = c.source_currency
		           AND st.amount = c.source_amount,
		           false
		       ),
		       COALESCE(
		           tt.id IS NOT NULL
		           AND tt.conversion_id = c.id
		           AND tt.fx_quote_id = c.quote_id
		           AND tt.fx_leg = 'target'
		           AND tt.counterpart_transaction_id = c.source_transaction_id
		           AND tt.type = 'fx_in'
		           AND tt.currency = c.target_currency
		           AND tt.amount = c.target_amount,
		           false
		       ),
		       COALESCE((
		           SELECT COUNT(*) > 0
		               AND COALESCE(SUM(le.amount) FILTER (WHERE le.direction = 'debit'), 0)
		                   = COALESCE(SUM(le.amount) FILTER (WHERE le.direction = 'credit'), 0)
		           FROM ledger_entries le
		           WHERE le.transaction_id = c.source_transaction_id
		       ), false),
		       COALESCE((
		           SELECT COUNT(*) > 0
		               AND COALESCE(SUM(le.amount) FILTER (WHERE le.direction = 'debit'), 0)
		                   = COALESCE(SUM(le.amount) FILTER (WHERE le.direction = 'credit'), 0)
		           FROM ledger_entries le
		           WHERE le.transaction_id = c.target_transaction_id
		       ), false)
		       , COALESCE((
		           SELECT su.id IS NOT NULL
		               AND sp.id IS NOT NULL
		               AND tp.id IS NOT NULL
		               AND tu.id IS NOT NULL
		           FROM accounts su
		           JOIN accounts sp ON sp.id = st.destination_account_id
		           JOIN accounts tp ON tp.id = tt.source_account_id
		           JOIN accounts tu ON tu.id = tt.destination_account_id
		           WHERE su.id = st.source_account_id
		             AND su.owner_type = 'user' AND su.owner_id = c.user_id
		             AND su.type = 'cash' AND su.currency = c.source_currency
		             AND su.pocket_code IS NULL AND su.status = 'active'
		             AND sp.owner_type = 'system' AND sp.type = 'fx_conversion'
		             AND sp.currency = c.source_currency
		             AND sp.system_qualifier = p.position_qualifier
		             AND sp.status = 'active'
		             AND tp.owner_type = 'system' AND tp.type = 'fx_conversion'
		             AND tp.currency = c.target_currency
		             AND tp.system_qualifier = p.position_qualifier
		             AND tp.status = 'active'
		             AND tu.owner_type = 'user' AND tu.owner_id = c.user_id
		             AND tu.type = 'cash' AND tu.currency = c.target_currency
		             AND tu.pocket_code IS NULL AND tu.status = 'active'
		       ), false),
		       COALESCE(EXISTS (
		           SELECT 1
		           FROM outbox_events oe
		           WHERE oe.aggregate_type = 'fx_conversion'
		             AND oe.aggregate_id = c.id
		             AND oe.event_type = 'ledger.fx_conversion.posted.v1'
		       ), false),
		       COALESCE((
		           SELECT ab.balance = COALESCE((
		               SELECT SUM(CASE WHEN le.direction = 'credit' THEN le.amount ELSE -le.amount END)
		               FROM ledger_entries le
		               JOIN ledger_transactions lt ON lt.id = le.transaction_id
		               WHERE le.account_id = st.destination_account_id
		                 AND lt.status = 'posted'
		           ), 0)
		           FROM account_balances ab
		           WHERE ab.account_id = st.destination_account_id
		       ), false)
		       AND COALESCE((
		           SELECT ab.balance = COALESCE((
		               SELECT SUM(CASE WHEN le.direction = 'credit' THEN le.amount ELSE -le.amount END)
		               FROM ledger_entries le
		               JOIN ledger_transactions lt ON lt.id = le.transaction_id
		               WHERE le.account_id = tt.source_account_id
		                 AND lt.status = 'posted'
		           ), 0)
		           FROM account_balances ab
		           WHERE ab.account_id = tt.source_account_id
		       ), false)
		FROM fx_conversions c
		LEFT JOIN fx_quotes q ON q.id = c.quote_id
		LEFT JOIN fx_pairs p ON p.id = q.pair_id
		LEFT JOIN ledger_transactions st ON st.id = c.source_transaction_id
		LEFT JOIN ledger_transactions tt ON tt.id = c.target_transaction_id
		WHERE COALESCE(c.posted_at, c.created_at) >= $1
		  AND COALESCE(c.posted_at, c.created_at) < $2
		ORDER BY COALESCE(c.posted_at, c.created_at), c.id
		LIMIT $3`, from, to, limit)
	if err != nil {
		return model.FXReconciliationReport{}, fmt.Errorf("query FX conversion reconciliation: %w", err)
	}
	defer rows.Close()

	report := model.FXReconciliationReport{
		From: from, To: to, Items: make([]model.FXConversionReconciliation, 0),
	}
	for rows.Next() {
		var item model.FXConversionReconciliation
		var conversionStatus, errorMessage string
		var sourceTransactionID, targetTransactionID uuid.NullUUID
		item.ResourceType = "conversion"
		if err := rows.Scan(
			&item.ConversionID, &item.QuoteID, &item.SourceCurrency, &item.TargetCurrency,
			&item.SourceAmount, &item.TargetAmount, &conversionStatus,
			&sourceTransactionID, &targetTransactionID,
			&item.SourceLegStatus, &item.TargetLegStatus, &errorMessage,
			&item.QuoteValid,
			&item.SourceLinkValid, &item.TargetLinkValid,
			&item.SourceLegBalanced, &item.TargetLegBalanced,
			&item.PositionAccountsValid, &item.AggregateEventPresent,
			&item.PositionBalancesValid,
		); err != nil {
			return model.FXReconciliationReport{}, fmt.Errorf("scan FX conversion reconciliation: %w", err)
		}
		item.ResourceID = item.ConversionID
		if sourceTransactionID.Valid {
			item.SourceTransactionID = sourceTransactionID.UUID
		}
		if targetTransactionID.Valid {
			item.TargetTransactionID = targetTransactionID.UUID
		}
		item.SourceCurrency = strings.TrimSpace(item.SourceCurrency)
		item.TargetCurrency = strings.TrimSpace(item.TargetCurrency)
		item.SourceLegStatus = strings.TrimSpace(item.SourceLegStatus)
		item.TargetLegStatus = strings.TrimSpace(item.TargetLegStatus)
		item.CheckedAt = now

		var reasons []string
		if conversionStatus == "posted" {
			if item.SourceLegStatus != "posted" {
				reasons = append(reasons, "source FX leg is missing or not posted")
			}
			if item.TargetLegStatus != "posted" {
				reasons = append(reasons, "target FX leg is missing or not posted")
			}
			if !item.SourceLinkValid {
				reasons = append(reasons, "source FX leg link/currency/amount is inconsistent")
			}
			if !item.TargetLinkValid {
				reasons = append(reasons, "target FX leg link/currency/amount is inconsistent")
			}
			if !item.QuoteValid {
				reasons = append(reasons, "quote is missing, not consumed by this conversion, or does not match")
			}
			if !item.PositionAccountsValid {
				reasons = append(reasons, "FX legs use the wrong user or position account")
			}
			if !item.SourceLegBalanced {
				reasons = append(reasons, "source FX leg is not independently balanced")
			}
			if !item.TargetLegBalanced {
				reasons = append(reasons, "target FX leg is not independently balanced")
			}
			if !item.PositionBalancesValid {
				reasons = append(reasons, "FX position balance projection disagrees with posted entries")
			}
			if !item.AggregateEventPresent {
				reasons = append(reasons, "aggregate FX outbox event is missing")
			}
			if len(reasons) == 0 {
				item.Status = "reconciled"
				report.Reconciled++
			} else {
				item.Status = "critical"
				report.Critical++
			}
		} else {
			item.Status = conversionStatus
			if conversionStatus == "pending" {
				reasons = append(reasons, "conversion remains pending")
				report.Critical++
			}
			if conversionStatus == "failed" && errorMessage != "" {
				reasons = append(reasons, "conversion failed: "+errorMessage)
			}
		}
		item.Reason = strings.Join(reasons, "; ")
		if item.Status == "critical" {
			fxAssuranceFindingsTotal.WithLabelValues(reconciliationFinding(item.Reason), "critical").Inc()
		}
		report.Items = append(report.Items, item)
	}
	if err := rows.Err(); err != nil {
		return model.FXReconciliationReport{}, fmt.Errorf("iterate FX conversion reconciliation: %w", err)
	}
	rows.Close()
	remaining := limit - len(report.Items)
	if remaining > 0 {
		if err := s.appendConsumedQuoteOrphans(ctx, from, to, remaining, now, &report); err != nil {
			return model.FXReconciliationReport{}, err
		}
	}
	remaining = limit - len(report.Items)
	if remaining > 0 {
		if err := s.appendUnlinkedFXLegs(ctx, from, to, remaining, now, &report); err != nil {
			return model.FXReconciliationReport{}, err
		}
	}
	report.Total = len(report.Items)
	return report, nil
}

func (s *Service) appendConsumedQuoteOrphans(ctx context.Context, from, to time.Time, limit int, checkedAt time.Time, report *model.FXReconciliationReport) error {
	rows, err := s.db.QueryContext(ctx, `
		SELECT q.id, q.consumed_by_conversion_id, q.source_currency, q.target_currency,
		       q.source_amount, q.target_amount
		FROM fx_quotes q
		WHERE q.status = 'consumed'
		  AND q.consumed_at >= $1
		  AND q.consumed_at < $2
		  AND NOT EXISTS (
		      SELECT 1 FROM fx_conversions c WHERE c.quote_id = q.id
		  )
		ORDER BY q.consumed_at, q.id
		LIMIT $3`, from, to, limit)
	if err != nil {
		return fmt.Errorf("query consumed FX quote orphans: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var item model.FXConversionReconciliation
		var quoteID, consumedBy uuid.NullUUID
		if err := rows.Scan(&quoteID, &consumedBy, &item.SourceCurrency, &item.TargetCurrency, &item.SourceAmount, &item.TargetAmount); err != nil {
			return fmt.Errorf("scan consumed FX quote orphan: %w", err)
		}
		item.ResourceType = "quote"
		item.ResourceID = quoteID.UUID
		item.QuoteID = quoteID.UUID
		if consumedBy.Valid {
			item.ConversionID = consumedBy.UUID
		}
		item.SourceCurrency = strings.TrimSpace(item.SourceCurrency)
		item.TargetCurrency = strings.TrimSpace(item.TargetCurrency)
		item.Status = "critical"
		item.Reason = "consumed FX quote has no conversion aggregate"
		item.CheckedAt = checkedAt
		item.AggregateEventPresent = false
		report.Critical++
		fxAssuranceFindingsTotal.WithLabelValues("consumed_quote_without_conversion", "critical").Inc()
		report.Items = append(report.Items, item)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate consumed FX quote orphans: %w", err)
	}
	return nil
}

func (s *Service) appendUnlinkedFXLegs(ctx context.Context, from, to time.Time, limit int, checkedAt time.Time, report *model.FXReconciliationReport) error {
	rows, err := s.db.QueryContext(ctx, `
		SELECT lt.id, lt.type, lt.currency, lt.amount, lt.status
		FROM ledger_transactions lt
		WHERE lt.type IN ('fx_out', 'fx_in')
		  AND lt.conversion_id IS NULL
		  AND lt.created_at >= $1
		  AND lt.created_at < $2
		ORDER BY lt.created_at, lt.id
		LIMIT $3`, from, to, limit)
	if err != nil {
		return fmt.Errorf("query unlinked FX legs: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var item model.FXConversionReconciliation
		var transactionID uuid.UUID
		var transactionType string
		if err := rows.Scan(&transactionID, &transactionType, &item.SourceCurrency, &item.SourceAmount, &item.SourceLegStatus); err != nil {
			return fmt.Errorf("scan unlinked FX leg: %w", err)
		}
		item.ResourceType = "transaction"
		item.ResourceID = transactionID
		item.SourceCurrency = strings.TrimSpace(item.SourceCurrency)
		item.Status = "critical"
		item.Reason = "FX leg has no conversion aggregate"
		item.CheckedAt = checkedAt
		item.AggregateEventPresent = false
		if transactionType == "fx_out" {
			item.SourceTransactionID = transactionID
			item.Reason = "FX source leg has no conversion aggregate"
		} else {
			item.TargetTransactionID = transactionID
			item.TargetCurrency = item.SourceCurrency
			item.TargetAmount = item.SourceAmount
			item.SourceCurrency = ""
			item.SourceAmount = 0
			item.Reason = "FX target leg has no conversion aggregate"
		}
		report.Critical++
		fxAssuranceFindingsTotal.WithLabelValues("unlinked_fx_leg", "critical").Inc()
		report.Items = append(report.Items, item)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate unlinked FX legs: %w", err)
	}
	return nil
}

func reconciliationFinding(reason string) string {
	switch {
	case strings.Contains(reason, "no conversion aggregate"):
		return "orphan_fx_resource"
	case strings.Contains(reason, "aggregate FX outbox event"):
		return "missing_aggregate_event"
	case strings.Contains(reason, "wrong user or position account"):
		return "wrong_position_account"
	case strings.Contains(reason, "position balance projection"):
		return "position_projection_mismatch"
	case strings.Contains(reason, "quote is missing"):
		return "quote_consumption_mismatch"
	case strings.Contains(reason, "missing or not posted"):
		return "missing_counterpart_leg"
	case strings.Contains(reason, "not independently balanced"):
		return "unbalanced_leg"
	case strings.Contains(reason, "link/currency/amount"):
		return "link_or_currency_mismatch"
	case strings.Contains(reason, "pending"):
		return "pending_conversion"
	default:
		return "conversion_integrity"
	}
}
