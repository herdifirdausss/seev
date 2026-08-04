package model

// TrialBalanceDiscrepancy is one unbalanced transaction found by
// fn_verify_ledger_balance — sum(debit) must always equal sum(credit); any
// row here is a serious bug and must be investigated.
type TrialBalanceDiscrepancy struct {
	TransactionID string
	SumDebit      int64
	SumCredit     int64
	Diff          int64
}

// ProjectionDiscrepancy is one account whose account_balances.balance
// doesn't match the balance computed from ledger_entries, found by
// v_account_balance_audit.
type ProjectionDiscrepancy struct {
	AccountID       string
	StoredBalance   int64
	ComputedBalance int64
}
