package ledger

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	ledgerv1 "github.com/herdifirdausss/seev/gen/go/ledger/v1"
	"github.com/herdifirdausss/seev/internal/platform/transport/httpcontract"
	"github.com/herdifirdausss/seev/internal/platform/transport/http/response"
)

const maxAssuranceFXWindow = 366 * 24 * time.Hour

// AssuranceRouter is the internal, read-only contract consumed by
// assurance-service. It is mounted behind the shared internal token and the
// Ledger listener's mTLS allowlist; it is deliberately separate from the
// admin router so Assurance does not need an operator JWT.
func (m *Module) AssuranceRouter() http.Handler {
	mux := httpcontract.New(httpcontract.Options{Owner: "ledger", Audience: "internal", Contract: "internal-v1"})
	mux.HandleFunc("GET /assurance/fx/reconciliation", m.handleAssuranceFXReconciliation)
	return mux
}

type assuranceFXReconciliationResponse struct {
	From       time.Time                       `json:"from"`
	To         time.Time                       `json:"to"`
	Total      int                             `json:"total"`
	Reconciled int                             `json:"reconciled"`
	Critical   int                             `json:"critical"`
	Items      []assuranceFXReconciliationItem `json:"items"`
}

type assuranceFXReconciliationItem struct {
	ResourceType          string    `json:"resource_type"`
	ResourceID            string    `json:"resource_id"`
	ConversionID          string    `json:"conversion_id"`
	QuoteID               string    `json:"quote_id"`
	SourceCurrency        string    `json:"source_currency"`
	TargetCurrency        string    `json:"target_currency"`
	SourceAmount          string    `json:"source_amount"`
	TargetAmount          string    `json:"target_amount"`
	SourceTransactionID   string    `json:"source_transaction_id,omitempty"`
	TargetTransactionID   string    `json:"target_transaction_id,omitempty"`
	SourceLegStatus       string    `json:"source_leg_status"`
	TargetLegStatus       string    `json:"target_leg_status"`
	SourceLinkValid       bool      `json:"source_link_valid"`
	TargetLinkValid       bool      `json:"target_link_valid"`
	SourceLegBalanced     bool      `json:"source_leg_balanced"`
	TargetLegBalanced     bool      `json:"target_leg_balanced"`
	QuoteValid            bool      `json:"quote_valid"`
	PositionAccountsValid bool      `json:"position_accounts_valid"`
	PositionBalancesValid bool      `json:"position_balances_valid"`
	AggregateEventPresent bool      `json:"aggregate_event_present"`
	Status                string    `json:"status"`
	Reason                string    `json:"reason"`
	CheckedAt             time.Time `json:"checked_at"`
}

func (m *Module) handleAssuranceFXReconciliation(w http.ResponseWriter, r *http.Request) {
	from, to, err := parseAssuranceFXWindow(r.URL.Query().Get("from"), r.URL.Query().Get("to"))
	if err != nil {
		response.BadRequest(w, err.Error())
		return
	}
	limit := 1000
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit <= 0 || limit > 1000 {
			response.BadRequest(w, "limit must be an integer between 1 and 1000")
			return
		}
	}
	report, err := m.ReconcileFXConversions(r.Context(), from, to, limit)
	if err != nil {
		response.InternalServerError(w, err)
		return
	}
	items := make([]assuranceFXReconciliationItem, 0, len(report.Items))
	for _, item := range report.Items {
		value := assuranceFXReconciliationItem{
			ResourceType: item.ResourceType, ResourceID: item.ResourceID.String(),
			ConversionID: item.ConversionID.String(), QuoteID: item.QuoteID.String(),
			SourceCurrency: item.SourceCurrency, TargetCurrency: item.TargetCurrency,
			SourceAmount: strconv.FormatInt(item.SourceAmount, 10), TargetAmount: strconv.FormatInt(item.TargetAmount, 10),
			SourceLegStatus: item.SourceLegStatus, TargetLegStatus: item.TargetLegStatus,
			SourceLinkValid: item.SourceLinkValid, TargetLinkValid: item.TargetLinkValid,
			SourceLegBalanced: item.SourceLegBalanced, TargetLegBalanced: item.TargetLegBalanced,
			QuoteValid: item.QuoteValid, PositionAccountsValid: item.PositionAccountsValid,
			PositionBalancesValid: item.PositionBalancesValid, AggregateEventPresent: item.AggregateEventPresent,
			Status: item.Status, Reason: item.Reason, CheckedAt: item.CheckedAt,
		}
		if item.SourceTransactionID != uuid.Nil {
			value.SourceTransactionID = item.SourceTransactionID.String()
		}
		if item.TargetTransactionID != uuid.Nil {
			value.TargetTransactionID = item.TargetTransactionID.String()
		}
		items = append(items, value)
	}
	response.OK(w, assuranceFXReconciliationResponse{
		From: from, To: to, Total: report.Total, Reconciled: report.Reconciled,
		Critical: report.Critical, Items: items,
	})
}

func parseAssuranceFXWindow(rawFrom, rawTo string) (time.Time, time.Time, error) {
	now := time.Now().UTC()
	from, to := now.Add(-24*time.Hour), now
	var err error
	if rawFrom != "" {
		from, err = time.Parse(time.RFC3339Nano, rawFrom)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("from must be RFC3339")
		}
	}
	if rawTo != "" {
		to, err = time.Parse(time.RFC3339Nano, rawTo)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("to must be RFC3339")
		}
	}
	if !to.After(from) {
		return time.Time{}, time.Time{}, fmt.Errorf("to must be after from")
	}
	if to.Sub(from) > maxAssuranceFXWindow {
		return time.Time{}, time.Time{}, fmt.Errorf("FX reconciliation window cannot exceed %s", maxAssuranceFXWindow)
	}
	return from.UTC(), to.UTC(), nil
}

func (m *Module) BatchGetAssuranceTransactions(ctx context.Context, req *ledgerv1.BatchGetAssuranceTransactionsRequest) (*ledgerv1.BatchGetAssuranceTransactionsResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if len(req.GetSelectors()) > 500 || len(req.GetFeeQuoteIds()) > 500 {
		return nil, status.Error(codes.InvalidArgument, "assurance batch limit is 500")
	}
	response := &ledgerv1.BatchGetAssuranceTransactionsResponse{Results: make([]*ledgerv1.AssuranceTransactionResult, 0, len(req.GetSelectors()))}
	for _, selector := range req.GetSelectors() {
		kind := 0
		if selector.GetTransactionId() != "" {
			kind++
		}
		if selector.GetIdempotencyKey() != "" {
			kind++
		}
		if selector.GetType() != "" || selector.GetGateway() != "" || selector.GetExternalRef() != "" {
			if selector.GetType() == "" || selector.GetGateway() == "" || selector.GetExternalRef() == "" {
				return nil, status.Error(codes.InvalidArgument, "correlation selector requires type, gateway, and external_ref")
			}
			kind++
		}
		if kind != 1 {
			return nil, status.Error(codes.InvalidArgument, "selector must contain exactly one lookup")
		}
		transactions, err := m.assuranceLookup(ctx, selector)
		if err != nil {
			return nil, err
		}
		result := &ledgerv1.AssuranceTransactionResult{Token: selector.GetToken(), Transactions: transactions}
		response.Results = append(response.Results, result)
	}
	for _, quoteID := range req.GetFeeQuoteIds() {
		id, err := uuid.Parse(quoteID)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "fee_quote_ids must be UUIDs")
		}
		var proof ledgerv1.FeeQuoteProof
		var amount, feeAmount int64
		var consumedAt sql.NullTime
		var consumedBy sql.NullString
		if err := m.db.QueryRowContext(ctx, `SELECT id, transaction_type, amount, currency, fee_amount, fee_gateway, consumed_at, consumed_by_ref FROM fee_quotes WHERE id = $1`, id).
			Scan(&proof.QuoteId, &proof.TransactionType, &amount, &proof.Currency, &feeAmount, &proof.FeeGateway, &consumedAt, &consumedBy); err != nil {
			if err == sql.ErrNoRows {
				continue
			}
			return nil, fmt.Errorf("lookup assurance fee quote: %w", err)
		}
		proof.Amount, proof.FeeAmount = fmt.Sprintf("%d", amount), fmt.Sprintf("%d", feeAmount)
		if consumedAt.Valid {
			proof.Status = "consumed"
		} else {
			proof.Status = "available"
		}
		if consumedBy.Valid {
			proof.ConsumedByRef = maskConsumedBy(consumedBy.String)
		}
		response.FeeQuoteProofs = append(response.FeeQuoteProofs, &proof)
	}
	return response, nil
}

func (m *Module) assuranceLookup(ctx context.Context, selector *ledgerv1.AssuranceSelector) ([]*ledgerv1.AssuranceTransaction, error) {
	// gateway/external_ref are nullable for internal lifecycle and legacy
	// transactions. The assurance wire contract is data-minimal and uses an
	// empty string for an absent correlation, so normalize NULL at the SQL
	// boundary instead of allowing one unrelated transaction to abort a page.
	query := `SELECT id, type, status, amount, currency, COALESCE(gateway, ''), COALESCE(external_ref, ''), closed_by_tx_id, closed_reason, created_at, updated_at FROM ledger_transactions WHERE `
	args := make([]any, 0, 3)
	switch {
	case selector.GetTransactionId() != "":
		id, err := uuid.Parse(selector.GetTransactionId())
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "transaction_id must be UUID")
		}
		query += "id = $1"
		args = append(args, id)
	case selector.GetIdempotencyKey() != "":
		query += "idempotency_key = $1 AND COALESCE(idempotency_scope, '') = COALESCE($2, '')"
		args = append(args, selector.GetIdempotencyKey(), selector.GetIdempotencyScope())
	default:
		query += "type = $1 AND gateway = $2 AND external_ref = $3"
		args = append(args, selector.GetType(), selector.GetGateway(), selector.GetExternalRef())
	}
	query += " ORDER BY created_at, id LIMIT 501"
	rows, err := m.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("lookup assurance transactions: %w", err)
	}
	defer rows.Close()
	var output []*ledgerv1.AssuranceTransaction
	for rows.Next() {
		var txID uuid.UUID
		var amount int64
		var txType, txStatus, currency, gateway, externalRef string
		var closedID, closedReason sql.NullString
		var created, updated time.Time
		if err := rows.Scan(&txID, &txType, &txStatus, &amount, &currency, &gateway, &externalRef, &closedID, &closedReason, &created, &updated); err != nil {
			return nil, fmt.Errorf("scan assurance transaction: %w", err)
		}
		feeAmount, feeGateway, err := m.bookedFee(ctx, txID)
		if err != nil {
			return nil, err
		}
		value := &ledgerv1.AssuranceTransaction{Id: txID.String(), Type: txType, Status: txStatus,
			Amount: fmt.Sprintf("%d", amount), Currency: currency, Gateway: gateway, ExternalRef: externalRef,
			BookedFeeAmount: fmt.Sprintf("%d", feeAmount), BookedFeeGateway: feeGateway,
			CreatedAt: timestamppb.New(created), UpdatedAt: timestamppb.New(updated)}
		if closedID.Valid {
			value.LifecycleCloserId = closedID.String
			value.LifecycleCloserReason = closedReason.String
		}
		var originalID string
		_ = m.db.QueryRowContext(ctx, `SELECT id::text FROM ledger_transactions WHERE closed_by_tx_id = $1`, txID).Scan(&originalID)
		value.OriginalReferenceId = originalID
		output = append(output, value)
	}
	return output, rows.Err()
}

func (m *Module) bookedFee(ctx context.Context, txID uuid.UUID) (int64, string, error) {
	var amount int64
	var gateway sql.NullString
	err := m.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(e.amount), 0), MAX(COALESCE(a.system_qualifier, ''))
		FROM ledger_entries e JOIN accounts a ON a.id = e.account_id
		WHERE e.transaction_id = $1 AND a.type = 'fee'`, txID).Scan(&amount, &gateway)
	if err != nil {
		return 0, "", fmt.Errorf("lookup booked fee: %w", err)
	}
	return amount, gateway.String, nil
}

func maskConsumedBy(value string) string {
	if strings.HasPrefix(value, "payout:") {
		return value
	}
	if len(value) > 64 {
		return value[:64]
	}
	return value
}
