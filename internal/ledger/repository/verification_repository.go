package repository

//go:generate mockgen -source=verification_repository.go -destination=verification_repository_mock.go -package=repository

import (
	"context"

	"github.com/herdifirdausss/seev/internal/ledger/model"
	"github.com/herdifirdausss/seev/pkg/database"
)

// VerificationRepository reads the ledger's own integrity-check functions
// and views (fn_verify_ledger_balance, v_account_balance_audit) — read-only,
// used only by worker.Verifier's scheduled checks (docs/roadmap/archive/06 Task 1c.2).
type VerificationRepository interface {
	// TrialBalanceDiscrepancies proves sum(debit) == sum(credit) for every
	// transaction posted in the last 2 hours — the window is computed
	// server-side by fn_verify_ledger_balance itself.
	TrialBalanceDiscrepancies(ctx context.Context) ([]model.TrialBalanceDiscrepancy, error)
	// ProjectionDiscrepancies proves account_balances.balance matches the
	// balance computed from ledger_entries, for every inconsistent account.
	ProjectionDiscrepancies(ctx context.Context) ([]model.ProjectionDiscrepancy, error)
}

type verificationRepo struct {
	db database.DatabaseSQL
}

func NewVerificationRepository(db database.DatabaseSQL) VerificationRepository {
	return &verificationRepo{db: db}
}

func (r *verificationRepo) TrialBalanceDiscrepancies(ctx context.Context) ([]model.TrialBalanceDiscrepancy, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT transaction_id, sum_debit, sum_credit, diff
		 FROM fn_verify_ledger_balance(now() - INTERVAL '2 hours', now())`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	discrepancies := make([]model.TrialBalanceDiscrepancy, 0)
	for rows.Next() {
		var d model.TrialBalanceDiscrepancy
		if err := rows.Scan(&d.TransactionID, &d.SumDebit, &d.SumCredit, &d.Diff); err != nil {
			return nil, err
		}
		discrepancies = append(discrepancies, d)
	}
	return discrepancies, rows.Err()
}

func (r *verificationRepo) ProjectionDiscrepancies(ctx context.Context) ([]model.ProjectionDiscrepancy, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT account_id, stored_balance, computed_balance
		 FROM v_account_balance_audit WHERE is_consistent = false`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	discrepancies := make([]model.ProjectionDiscrepancy, 0)
	for rows.Next() {
		var d model.ProjectionDiscrepancy
		if err := rows.Scan(&d.AccountID, &d.StoredBalance, &d.ComputedBalance); err != nil {
			return nil, err
		}
		discrepancies = append(discrepancies, d)
	}
	return discrepancies, rows.Err()
}
