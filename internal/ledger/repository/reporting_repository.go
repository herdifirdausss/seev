package repository

//go:generate mockgen -source=reporting_repository.go -destination=reporting_repository_mock.go -package=repository

import (
	"context"
	"fmt"
	"time"

	"github.com/shopspring/decimal"

	"github.com/herdifirdausss/seev/internal/ledger/model"
	"github.com/herdifirdausss/seev/pkg/database"
)

// ReportingRepository reads the three regulatory-reporting views
// (docs/roadmap/archive/20 Task T2, migrations/000018) — read-only, no writes anywhere
// in this file. Queries run through the normal app_service pooled
// connection (the views themselves are the access-control boundary for
// external app_readonly tools, not this repository — see the migration's
// own comment).
type ReportingRepository interface {
	DailyPosition(ctx context.Context, from, to time.Time) ([]model.ReportDailyPosition, error)
	DailyMutation(ctx context.Context, from, to time.Time) ([]model.ReportDailyMutation, error)
	ReconSummary(ctx context.Context, from, to time.Time) ([]model.ReportReconSummary, error)
}

type reportingRepo struct {
	db database.DatabaseSQL
}

func NewReportingRepository(db database.DatabaseSQL) ReportingRepository {
	return &reportingRepo{db: db}
}

func (r *reportingRepo) DailyPosition(ctx context.Context, from, to time.Time) ([]model.ReportDailyPosition, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT as_of_date, currency, account_type, owner_type, account_count, total_balance
		FROM v_report_daily_position
		WHERE as_of_date BETWEEN $1 AND $2
		ORDER BY as_of_date, currency, account_type, owner_type`,
		from.Format(dateLayout), to.Format(dateLayout))
	if err != nil {
		return nil, fmt.Errorf("query v_report_daily_position: %w", err)
	}
	defer rows.Close()

	var out []model.ReportDailyPosition
	for rows.Next() {
		var row model.ReportDailyPosition
		var totalBalance int64
		if err := rows.Scan(&row.AsOfDate, &row.Currency, &row.AccountType, &row.OwnerType, &row.AccountCount, &totalBalance); err != nil {
			return nil, fmt.Errorf("scan v_report_daily_position: %w", err)
		}
		row.TotalBalance = decimal.NewFromInt(totalBalance)
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate v_report_daily_position: %w", err)
	}
	return out, nil
}

func (r *reportingRepo) DailyMutation(ctx context.Context, from, to time.Time) ([]model.ReportDailyMutation, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT report_date, tx_type, currency, tx_count, total_amount
		FROM v_report_daily_mutation
		WHERE report_date BETWEEN $1 AND $2
		ORDER BY report_date, tx_type, currency`,
		from.Format(dateLayout), to.Format(dateLayout))
	if err != nil {
		return nil, fmt.Errorf("query v_report_daily_mutation: %w", err)
	}
	defer rows.Close()

	var out []model.ReportDailyMutation
	for rows.Next() {
		var row model.ReportDailyMutation
		var totalAmount int64
		if err := rows.Scan(&row.ReportDate, &row.TxType, &row.Currency, &row.TxCount, &totalAmount); err != nil {
			return nil, fmt.Errorf("scan v_report_daily_mutation: %w", err)
		}
		row.TotalAmount = decimal.NewFromInt(totalAmount)
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate v_report_daily_mutation: %w", err)
	}
	return out, nil
}

func (r *reportingRepo) ReconSummary(ctx context.Context, from, to time.Time) ([]model.ReportReconSummary, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT batch_id, gateway, report_date, source_filename, batch_status, declared_row_count,
		       item_count, matched_count, missing_internal_count, missing_external_count,
		       amount_mismatch_count, resolved_count
		FROM v_report_recon_summary
		WHERE report_date BETWEEN $1 AND $2
		ORDER BY report_date, gateway`,
		from.Format(dateLayout), to.Format(dateLayout))
	if err != nil {
		return nil, fmt.Errorf("query v_report_recon_summary: %w", err)
	}
	defer rows.Close()

	var out []model.ReportReconSummary
	for rows.Next() {
		var row model.ReportReconSummary
		if err := rows.Scan(&row.BatchID, &row.Gateway, &row.ReportDate, &row.SourceFilename, &row.BatchStatus,
			&row.DeclaredRowCount, &row.ItemCount, &row.MatchedCount, &row.MissingInternalCount,
			&row.MissingExternalCount, &row.AmountMismatchCount, &row.ResolvedCount); err != nil {
			return nil, fmt.Errorf("scan v_report_recon_summary: %w", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate v_report_recon_summary: %w", err)
	}
	return out, nil
}
