package transport

import (
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/herdifirdausss/seev/internal/ledger/feepolicy"
	"github.com/herdifirdausss/seev/internal/ledger/model"
)

type feeRuleRequest struct {
	TxType          string `json:"tx_type"`
	Gateway         string `json:"gateway"`
	Currency        string `json:"currency"`
	UserID          string `json:"user_id,omitempty"`
	FlatMinorUnits  int64  `json:"flat_minor_units"`
	PercentBasisPts int64  `json:"percent_basis_pts"`
	FeeGateway      string `json:"fee_gateway,omitempty"`
	Enabled         *bool  `json:"enabled,omitempty"`
}

type feeRuleResponse struct {
	ID              uuid.UUID  `json:"id"`
	TxType          string     `json:"tx_type"`
	Gateway         string     `json:"gateway"`
	Currency        string     `json:"currency"`
	UserID          *uuid.UUID `json:"user_id,omitempty"`
	FlatMinorUnits  int64      `json:"flat_minor_units"`
	PercentBasisPts int64      `json:"percent_basis_pts"`
	FeeGateway      string     `json:"fee_gateway"`
	Enabled         bool       `json:"enabled"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

func toFeeRuleResponse(rule feepolicy.Rule) feeRuleResponse {
	return feeRuleResponse{
		ID: rule.ID, TxType: rule.TxType, Gateway: rule.Gateway, Currency: rule.Currency,
		UserID: rule.UserID, FlatMinorUnits: rule.FlatMinorUnits,
		PercentBasisPts: rule.PercentBasisPts, FeeGateway: rule.FeeGateway,
		Enabled: rule.Enabled, CreatedAt: rule.CreatedAt, UpdatedAt: rule.UpdatedAt,
	}
}

type postTransactionRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
	// IdempotencyScope is only honored on the internal router — the public
	// router always forces scope = the caller's own userID (see
	// handler.postTransaction, docs/roadmap/archive/10 Task T2).
	IdempotencyScope string         `json:"idempotency_scope,omitempty"`
	Type             string         `json:"type"`
	Amount           string         `json:"amount"`
	TargetUserID     string         `json:"target_user_id,omitempty"`
	PocketCode       string         `json:"pocket_code,omitempty"`
	ReferenceID      string         `json:"reference_id,omitempty"`
	Metadata         map[string]any `json:"metadata,omitempty"`
	// QuoteID (docs/roadmap/archive/38 Task T4) is a typed field, deliberately NOT part
	// of Metadata — buildMetadata strips unknown metadata keys on the
	// public router (gotcha #5 master doc 36), so a quote_id smuggled into
	// Metadata would silently vanish before ever reaching execTransfer.
	QuoteID string `json:"quote_id,omitempty"`
}

type postTransactionResponse struct {
	Status         string `json:"status"`
	IdempotencyKey string `json:"idempotency_key"`
}

type createPocketRequest struct {
	Currency   string `json:"currency"`
	PocketCode string `json:"pocket_code"`
}

// ─── Admin: outbox dead-letter replay (docs/roadmap/archive/12 Task T3) ──────────────────

type replayDeadEventResponse struct {
	Replayed bool `json:"replayed"`
}

type replayAllDeadRequest struct {
	// OlderThan is an RFC3339 timestamp; events created before it are
	// replayed. Empty defaults to time.Now() — replay every currently-dead
	// event (capped at maxReplayAllBatch per call).
	OlderThan string `json:"older_than,omitempty"`
}

type replayAllDeadResponse struct {
	ReplayedCount int `json:"replayed_count"`
}

// ─── Admin: outbox dead-letter list (docs/roadmap/archive/25 Task T5) ────────────────────

type deadOutboxEventResponse struct {
	ID         uuid.UUID `json:"id"`
	EventType  string    `json:"event_type"`
	RetryCount int       `json:"retry_count"`
	LastError  string    `json:"last_error,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

func toDeadOutboxEventResponse(e model.DeadOutboxEvent) deadOutboxEventResponse {
	return deadOutboxEventResponse{
		ID: e.ID, EventType: e.EventType, RetryCount: e.RetryCount, LastError: e.LastError, CreatedAt: e.CreatedAt,
	}
}

type listDeadOutboxEventsResponse struct {
	Events []deadOutboxEventResponse `json:"events"`
}

// ─── Admin: recon batch list (docs/roadmap/archive/25 Task T5) ───────────────────────────
// Reuses reconBatchResponse (defined above alongside reconBatchReportResponse)
// — same shape, no need for a second near-identical DTO.

func toReconBatchListResponse(b model.ReconBatch) reconBatchResponse {
	return reconBatchResponse{
		ID: b.ID, Gateway: b.Gateway, ReportDate: b.ReportDate.Format("2006-01-02"),
		SourceFilename: b.SourceFilename, RowCount: b.RowCount, Status: b.Status,
		CreatedBy: b.CreatedBy, CreatedAt: b.CreatedAt,
	}
}

type listReconBatchesResponse struct {
	Batches []reconBatchResponse `json:"batches"`
}

type accountResponse struct {
	ID         uuid.UUID `json:"id"`
	OwnerID    uuid.UUID `json:"owner_id"`
	Type       string    `json:"type"`
	Currency   string    `json:"currency"`
	PocketCode string    `json:"pocket_code,omitempty"`
	Status     string    `json:"status"`
}

func toAccountResponse(a model.Account) accountResponse {
	return accountResponse{
		ID: a.ID, OwnerID: a.OwnerID, Type: a.Type,
		Currency: a.Currency, PocketCode: a.PocketCode, Status: a.Status,
	}
}

type balanceResponse struct {
	AccountID uuid.UUID `json:"account_id"`
	Currency  string    `json:"currency"`
	Balance   string    `json:"balance"`
	Status    string    `json:"status"`
	Type      string    `json:"type"`
	// AsOf is set only when the request specified ?as_of=YYYY-MM-DD
	// (docs/roadmap/archive/15 Task T1) — its absence means Balance is the CURRENT
	// balance, not a historical one.
	AsOf string `json:"as_of,omitempty"`
}

func toBalanceResponse(b model.AccountBalance) balanceResponse {
	return balanceResponse{
		AccountID: b.AccountID, Currency: b.Currency,
		Balance: b.Balance.String(), Status: b.Status, Type: b.Type,
	}
}

type transactionResponse struct {
	ID                   uuid.UUID `json:"id"`
	Type                 string    `json:"type"`
	Status               string    `json:"status"`
	Amount               string    `json:"amount"`
	Currency             string    `json:"currency"`
	SourceAccountID      uuid.UUID `json:"source_account_id,omitempty"`
	DestinationAccountID uuid.UUID `json:"destination_account_id,omitempty"`
	ErrorMessage         string    `json:"error_message,omitempty"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}

func toTransactionResponse(t model.LedgerTransaction) transactionResponse {
	return transactionResponse{
		ID: t.ID, Type: t.Type, Status: t.Status, Amount: t.Amount.String(), Currency: t.Currency,
		SourceAccountID: t.SourceAccountID, DestinationAccountID: t.DestinationAccountID,
		ErrorMessage: t.ErrorMessage, CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt,
	}
}

type entryResponse struct {
	ID            uuid.UUID `json:"id"`
	TransactionID uuid.UUID `json:"transaction_id"`
	AccountID     uuid.UUID `json:"account_id"`
	Direction     string    `json:"direction"`
	Amount        string    `json:"amount"`
	BalanceAfter  string    `json:"balance_after"`
	Note          string    `json:"note,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

func toEntryResponse(e model.LedgerEntry) entryResponse {
	return entryResponse{
		ID: e.ID, TransactionID: e.TransactionID, AccountID: e.AccountID,
		Direction: string(e.Direction), Amount: e.Amount.String(),
		BalanceAfter: e.BalanceAfter.String(), Note: e.Note, CreatedAt: e.CreatedAt,
	}
}

type listEntriesResponse struct {
	Entries    []entryResponse `json:"entries"`
	NextCursor string          `json:"next_cursor,omitempty"`
}

// statementEntryResponse mirrors entryResponse plus TransactionType — a
// statement line needs to say WHAT kind of transaction moved the money
// (docs/roadmap/archive/15 Task T2), which the plain entry-listing API doesn't.
type statementEntryResponse struct {
	EntryID         uuid.UUID `json:"entry_id"`
	TxID            uuid.UUID `json:"tx_id"`
	TransactionType string    `json:"transaction_type"`
	Direction       string    `json:"direction"`
	Amount          string    `json:"amount"`
	BalanceAfter    string    `json:"balance_after"`
	Note            string    `json:"note,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

func toStatementEntryResponse(e model.StatementEntry) statementEntryResponse {
	return statementEntryResponse{
		EntryID: e.ID, TxID: e.TransactionID, TransactionType: e.TransactionType,
		Direction: string(e.Direction), Amount: e.Amount.String(),
		BalanceAfter: e.BalanceAfter.String(), Note: e.Note, CreatedAt: e.CreatedAt,
	}
}

type statementResponse struct {
	AccountID      uuid.UUID                `json:"account_id"`
	Currency       string                   `json:"currency"`
	From           string                   `json:"from"`
	To             string                   `json:"to"`
	OpeningBalance string                   `json:"opening_balance"`
	ClosingBalance string                   `json:"closing_balance"`
	Entries        []statementEntryResponse `json:"entries"`
}

func toStatementResponse(s model.Statement) statementResponse {
	entries := make([]statementEntryResponse, len(s.Entries))
	for i, e := range s.Entries {
		entries[i] = toStatementEntryResponse(e)
	}
	return statementResponse{
		AccountID: s.AccountID, Currency: s.Currency,
		From: s.From.Format("2006-01-02"), To: s.To.Format("2006-01-02"),
		OpeningBalance: s.OpeningBalance.String(), ClosingBalance: s.ClosingBalance.String(),
		Entries: entries,
	}
}

// errNonIntegralAmount is returned when a request supplies a fractional
// amount. The ledger is minor-unit-only (docs/roadmap/archive/01 decision D2, e.g. IDR
// has 0 minor-unit digits) — accepting "100.75" and silently truncating it
// downstream would create or destroy money (docs/roadmap/archive/10 Task T4).
var errNonIntegralAmount = errors.New("amount must be an integer (minor units, no fractional part)")

// decimalFromString parses a required positive-amount field from a request
// body and rejects any fractional value.
func decimalFromString(s string) (decimal.Decimal, error) {
	amt, err := decimal.NewFromString(s)
	if err != nil {
		return decimal.Decimal{}, err
	}
	if !amt.Equal(amt.Truncate(0)) {
		return decimal.Decimal{}, errNonIntegralAmount
	}
	return amt, nil
}

// ─── Fee quotes (docs/roadmap/archive/38 Task T3) ──────────────────────────────────────

type quoteRequest struct {
	TransactionType string `json:"transaction_type"`
	Amount          string `json:"amount"`
	Currency        string `json:"currency,omitempty"`
	Gateway         string `json:"gateway,omitempty"`
}

// quoteResponse's Amount/FeeAmount/TotalDebit are decimal strings — same
// convention as every other money field in this API (docs/roadmap/archive/10).
type quoteResponse struct {
	QuoteID    uuid.UUID `json:"quote_id"`
	Amount     string    `json:"amount"`
	FeeAmount  string    `json:"fee_amount"`
	FeeGateway string    `json:"fee_gateway,omitempty"`
	TotalDebit string    `json:"total_debit"`
	Currency   string    `json:"currency"`
	ExpiresAt  time.Time `json:"expires_at"`
}

func toQuoteResponse(q feepolicy.Quote) quoteResponse {
	return quoteResponse{
		QuoteID: q.ID, Amount: q.Amount.String(), FeeAmount: q.FeeAmount.String(),
		FeeGateway: q.FeeGateway, TotalDebit: q.Amount.Add(q.FeeAmount).String(),
		Currency: q.Currency, ExpiresAt: q.ExpiresAt,
	}
}

// ─── Maker-checker adjustments (docs/roadmap/archive/16 Task T1) ──────────────────────

type createAdjustmentRequest struct {
	Type     string         `json:"type"`
	Amount   string         `json:"amount"`
	UserID   string         `json:"user_id"`
	Metadata map[string]any `json:"metadata,omitempty"`
	Reason   string         `json:"reason"`
}

type createAdjustmentResponse struct {
	ID uuid.UUID `json:"id"`
}

type approveAdjustmentResponse struct {
	ExecutedTxID uuid.UUID `json:"executed_tx_id"`
}

type rejectAdjustmentResponse struct {
	Rejected bool `json:"rejected"`
}

type adjustmentResponse struct {
	ID           uuid.UUID  `json:"id"`
	RequestedBy  string     `json:"requested_by"`
	ApprovedBy   string     `json:"approved_by,omitempty"`
	Reason       string     `json:"reason"`
	Status       string     `json:"status"`
	ExecutedTxID uuid.UUID  `json:"executed_tx_id,omitempty"`
	ErrorMessage string     `json:"error_message,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	DecidedAt    *time.Time `json:"decided_at,omitempty"`
}

func toAdjustmentResponse(pa model.PendingAdjustment) adjustmentResponse {
	out := adjustmentResponse{
		ID: pa.ID, RequestedBy: pa.RequestedBy, Reason: pa.Reason,
		Status: pa.Status, CreatedAt: pa.CreatedAt, DecidedAt: pa.DecidedAt,
	}
	if pa.ApprovedBy != nil {
		out.ApprovedBy = *pa.ApprovedBy
	}
	if pa.ExecutedTxID != nil {
		out.ExecutedTxID = *pa.ExecutedTxID
	}
	if pa.ErrorMessage != nil {
		out.ErrorMessage = *pa.ErrorMessage
	}
	return out
}

type listAdjustmentsResponse struct {
	Adjustments []adjustmentResponse `json:"adjustments"`
}

// ─── External reconciliation (docs/roadmap/archive/16 Task T2) ────────────────────────

type createReconBatchResponse struct {
	ID uuid.UUID `json:"id"`
}

type reconItemResponse struct {
	ID                     uuid.UUID `json:"id"`
	BatchID                uuid.UUID `json:"batch_id"`
	ExternalRef            string    `json:"external_ref"`
	Amount                 string    `json:"amount"`
	MatchStatus            string    `json:"match_status"`
	MatchedTxID            uuid.UUID `json:"matched_tx_id,omitempty"`
	ResolvedByAdjustmentID uuid.UUID `json:"resolved_by_adjustment_id,omitempty"`
	CreatedAt              time.Time `json:"created_at"`
}

func toReconItemResponse(it model.ReconItem) reconItemResponse {
	out := reconItemResponse{
		ID: it.ID, BatchID: it.BatchID, ExternalRef: it.ExternalRef,
		Amount: it.Amount.String(), MatchStatus: it.MatchStatus, CreatedAt: it.CreatedAt,
	}
	if it.MatchedTxID != nil {
		out.MatchedTxID = *it.MatchedTxID
	}
	if it.ResolvedByAdjustmentID != nil {
		out.ResolvedByAdjustmentID = *it.ResolvedByAdjustmentID
	}
	return out
}

type reconBatchResponse struct {
	ID             uuid.UUID `json:"id"`
	Gateway        string    `json:"gateway"`
	ReportDate     string    `json:"report_date"`
	SourceFilename string    `json:"source_filename"`
	RowCount       int       `json:"row_count"`
	Status         string    `json:"status"`
	CreatedBy      string    `json:"created_by"`
	CreatedAt      time.Time `json:"created_at"`
}

type reconBatchReportResponse struct {
	Batch  reconBatchResponse  `json:"batch"`
	Counts map[string]int      `json:"counts"`
	Items  []reconItemResponse `json:"items"`
}

func toReconBatchReportResponse(r model.ReconBatchReport) reconBatchReportResponse {
	items := make([]reconItemResponse, len(r.Items))
	for i, it := range r.Items {
		items[i] = toReconItemResponse(it)
	}
	return reconBatchReportResponse{
		Batch: reconBatchResponse{
			ID: r.Batch.ID, Gateway: r.Batch.Gateway, ReportDate: r.Batch.ReportDate.Format("2006-01-02"),
			SourceFilename: r.Batch.SourceFilename, RowCount: r.Batch.RowCount,
			Status: r.Batch.Status, CreatedBy: r.Batch.CreatedBy, CreatedAt: r.Batch.CreatedAt,
		},
		Counts: r.Counts,
		Items:  items,
	}
}

type resolveReconItemRequest struct {
	Type string `json:"type"`
	// Amount is optional — empty means "use the recon item's own amount"
	// (transport/http.go resolveReconItem).
	Amount string `json:"amount,omitempty"`
	Reason string `json:"reason,omitempty"`
}

type resolveReconItemResponse struct {
	AdjustmentID uuid.UUID `json:"adjustment_id"`
}

// ─── Scheduled transactions (docs/roadmap/archive/19 Task T1) ─────────────────────────

type createScheduleRequest struct {
	Type         string         `json:"type"`
	Amount       string         `json:"amount"`
	TargetUserID string         `json:"target_user_id,omitempty"`
	PocketCode   string         `json:"pocket_code,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`
	ScheduleKind string         `json:"schedule_kind"`
	// RunAtDate is YYYY-MM-DD.
	RunAtDate string `json:"run_at_date"`
	// DayOfMonth is required for schedule_kind="monthly" (1-28), omitted otherwise.
	DayOfMonth *int `json:"day_of_month,omitempty"`
}

type createScheduleResponse struct {
	ID uuid.UUID `json:"id"`
}

type scheduleResponse struct {
	ID           uuid.UUID `json:"id"`
	UserID       uuid.UUID `json:"user_id"`
	ScheduleKind string    `json:"schedule_kind"`
	RunAtDate    string    `json:"run_at_date"`
	DayOfMonth   *int      `json:"day_of_month,omitempty"`
	Status       string    `json:"status"`
	LastRunDate  string    `json:"last_run_date,omitempty"`
	LastError    string    `json:"last_error,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func toScheduleResponse(st model.ScheduledTransaction) scheduleResponse {
	out := scheduleResponse{
		ID: st.ID, UserID: st.UserID, ScheduleKind: st.ScheduleKind,
		RunAtDate: st.RunAtDate.Format("2006-01-02"), DayOfMonth: st.DayOfMonth,
		Status: st.Status, CreatedAt: st.CreatedAt, UpdatedAt: st.UpdatedAt,
	}
	if st.LastRunDate != nil {
		out.LastRunDate = st.LastRunDate.Format("2006-01-02")
	}
	if st.LastError != nil {
		out.LastError = *st.LastError
	}
	return out
}

type listSchedulesResponse struct {
	Schedules []scheduleResponse `json:"schedules"`
}

type pauseScheduleResponse struct {
	Paused bool `json:"paused"`
}

type resumeScheduleResponse struct {
	Resumed bool `json:"resumed"`
}

type cancelScheduleResponse struct {
	Cancelled bool `json:"cancelled"`
}

type runSchedulesResponse struct {
	Executed int `json:"executed"`
	Failed   int `json:"failed"`
}

// ─── Batch disbursement (docs/roadmap/archive/19 Task T2) ─────────────────────────────

type createDisbursementBatchResponse struct {
	ID uuid.UUID `json:"id"`
}

type disbursementItemResponse struct {
	ID         uuid.UUID `json:"id"`
	BatchID    uuid.UUID `json:"batch_id"`
	ItemNo     int       `json:"item_no"`
	UserID     uuid.UUID `json:"user_id"`
	Amount     string    `json:"amount"`
	Note       string    `json:"note,omitempty"`
	Status     string    `json:"status"`
	Error      string    `json:"error,omitempty"`
	PostedTxID uuid.UUID `json:"posted_tx_id,omitempty"`
}

func toDisbursementItemResponse(it model.DisbursementItem) disbursementItemResponse {
	out := disbursementItemResponse{
		ID: it.ID, BatchID: it.BatchID, ItemNo: it.ItemNo, UserID: it.UserID,
		Amount: it.Amount.String(), Note: it.Note, Status: it.Status,
	}
	if it.Error != nil {
		out.Error = *it.Error
	}
	if it.PostedTxID != nil {
		out.PostedTxID = *it.PostedTxID
	}
	return out
}

type disbursementBatchResponse struct {
	ID             uuid.UUID `json:"id"`
	SourceFilename string    `json:"source_filename"`
	RowCount       int       `json:"row_count"`
	Status         string    `json:"status"`
	CreatedBy      string    `json:"created_by"`
	CreatedAt      time.Time `json:"created_at"`
}

type disbursementBatchReportResponse struct {
	Batch  disbursementBatchResponse  `json:"batch"`
	Counts map[string]int             `json:"counts"`
	Items  []disbursementItemResponse `json:"items"`
}

func toDisbursementBatchReportResponse(r model.DisbursementBatchReport) disbursementBatchReportResponse {
	items := make([]disbursementItemResponse, len(r.Items))
	for i, it := range r.Items {
		items[i] = toDisbursementItemResponse(it)
	}
	return disbursementBatchReportResponse{
		Batch: disbursementBatchResponse{
			ID: r.Batch.ID, SourceFilename: r.Batch.SourceFilename, RowCount: r.Batch.RowCount,
			Status: r.Batch.Status, CreatedBy: r.Batch.CreatedBy, CreatedAt: r.Batch.CreatedAt,
		},
		Counts: r.Counts,
		Items:  items,
	}
}

type runDisbursementResponse struct {
	Processed int  `json:"processed"`
	Posted    int  `json:"posted"`
	Failed    int  `json:"failed"`
	Done      bool `json:"done"`
}

func toRunDisbursementResponse(r model.DisbursementRunResult) runDisbursementResponse {
	return runDisbursementResponse{Processed: r.Processed, Posted: r.Posted, Failed: r.Failed, Done: r.Done}
}

// ─── Interest accrual (docs/roadmap/archive/19 Task T3) ───────────────────────────────

type setSavingsConfigRequest struct {
	AnnualRateBps int  `json:"annual_rate_bps"`
	Enabled       bool `json:"enabled"`
}

type setSavingsConfigResponse struct {
	Set bool `json:"set"`
}

type savingsConfigResponse struct {
	AccountID     uuid.UUID `json:"account_id"`
	AnnualRateBps int       `json:"annual_rate_bps"`
	Enabled       bool      `json:"enabled"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func toSavingsConfigResponse(cfg model.SavingsConfig) savingsConfigResponse {
	return savingsConfigResponse{
		AccountID: cfg.AccountID, AnnualRateBps: cfg.AnnualRateBps, Enabled: cfg.Enabled,
		CreatedAt: cfg.CreatedAt, UpdatedAt: cfg.UpdatedAt,
	}
}

type listSavingsConfigsResponse struct {
	Configs []savingsConfigResponse `json:"configs"`
}

// ─── AML/fraud screening (docs/roadmap/archive/20 Task T1) ────────────────────────────

// ─── Regulatory reporting (docs/roadmap/archive/20 Task T2) ───────────────────────────

type dailyPositionResponse struct {
	AsOfDate     string `json:"as_of_date"`
	Currency     string `json:"currency"`
	AccountType  string `json:"account_type"`
	OwnerType    string `json:"owner_type"`
	AccountCount int    `json:"account_count"`
	TotalBalance string `json:"total_balance"`
}

func toDailyPositionResponse(row model.ReportDailyPosition) dailyPositionResponse {
	return dailyPositionResponse{
		AsOfDate: row.AsOfDate.Format("2006-01-02"), Currency: row.Currency,
		AccountType: row.AccountType, OwnerType: row.OwnerType,
		AccountCount: row.AccountCount, TotalBalance: row.TotalBalance.String(),
	}
}

type listDailyPositionResponse struct {
	Rows []dailyPositionResponse `json:"rows"`
}

type dailyMutationResponse struct {
	ReportDate  string `json:"report_date"`
	TxType      string `json:"tx_type"`
	Currency    string `json:"currency"`
	TxCount     int    `json:"tx_count"`
	TotalAmount string `json:"total_amount"`
}

func toDailyMutationResponse(row model.ReportDailyMutation) dailyMutationResponse {
	return dailyMutationResponse{
		ReportDate: row.ReportDate.Format("2006-01-02"), TxType: row.TxType, Currency: row.Currency,
		TxCount: row.TxCount, TotalAmount: row.TotalAmount.String(),
	}
}

type listDailyMutationResponse struct {
	Rows []dailyMutationResponse `json:"rows"`
}

type reconSummaryResponse struct {
	BatchID              uuid.UUID `json:"batch_id"`
	Gateway              string    `json:"gateway"`
	ReportDate           string    `json:"report_date"`
	SourceFilename       string    `json:"source_filename"`
	BatchStatus          string    `json:"batch_status"`
	DeclaredRowCount     int       `json:"declared_row_count"`
	ItemCount            int       `json:"item_count"`
	MatchedCount         int       `json:"matched_count"`
	MissingInternalCount int       `json:"missing_internal_count"`
	MissingExternalCount int       `json:"missing_external_count"`
	AmountMismatchCount  int       `json:"amount_mismatch_count"`
	ResolvedCount        int       `json:"resolved_count"`
}

func toReconSummaryResponse(row model.ReportReconSummary) reconSummaryResponse {
	return reconSummaryResponse{
		BatchID: row.BatchID, Gateway: row.Gateway, ReportDate: row.ReportDate.Format("2006-01-02"),
		SourceFilename: row.SourceFilename, BatchStatus: row.BatchStatus, DeclaredRowCount: row.DeclaredRowCount,
		ItemCount: row.ItemCount, MatchedCount: row.MatchedCount, MissingInternalCount: row.MissingInternalCount,
		MissingExternalCount: row.MissingExternalCount, AmountMismatchCount: row.AmountMismatchCount,
		ResolvedCount: row.ResolvedCount,
	}
}

type listReconSummaryResponse struct {
	Rows []reconSummaryResponse `json:"rows"`
}
