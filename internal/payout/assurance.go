package payout

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	payoutv1 "github.com/herdifirdausss/seev/gen/payout/v1"
)

func (m *Module) ListAssuranceRecords(ctx context.Context, req *payoutv1.ListAssuranceRecordsRequest) (*payoutv1.ListAssuranceRecordsResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	pageSize := int(req.GetPageSize())
	if pageSize < 0 {
		return nil, status.Error(codes.InvalidArgument, "page_size must be non-negative")
	}
	if pageSize == 0 {
		pageSize = 500
	}
	if pageSize > 500 {
		return nil, status.Error(codes.InvalidArgument, "page_size must be <= 500")
	}
	if req.GetCutoff() == nil || !req.GetCutoff().IsValid() {
		return nil, status.Error(codes.InvalidArgument, "cutoff is required")
	}
	var cursorTime time.Time
	var cursorID uuid.UUID
	if (req.GetCursorUpdatedAt() == nil) != (req.GetCursorId() == "") {
		return nil, status.Error(codes.InvalidArgument, "cursor_updated_at and cursor_id must be provided together")
	}
	if req.GetCursorUpdatedAt() != nil {
		if !req.GetCursorUpdatedAt().IsValid() {
			return nil, status.Error(codes.InvalidArgument, "cursor_updated_at is invalid")
		}
		var err error
		cursorID, err = uuid.Parse(req.GetCursorId())
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "cursor_id must be a UUID")
		}
		cursorTime = req.GetCursorUpdatedAt().AsTime()
	}
	const query = `
		SELECT p.id, p.created_at, p.status, p.user_id, p.amount, p.currency, p.vendor,
		       p.hold_tx_id, p.settle_tx_id, COALESCE(p.request_id, ''),
		       (p.request_id IS NOT NULL AND p.request_id <> ''),
		       COALESCE(p.fee_quote_id::text, ''), COALESCE(p.fee_amount, 0),
		       COALESCE(p.fee_gateway, ''),
		       GREATEST(p.updated_at,
		          COALESCE((SELECT max(created_at) FROM payout_vendor_calls c WHERE c.payout_request_id = p.id), p.updated_at),
		          COALESCE((SELECT max(updated_at) FROM payout_vendor_commands c WHERE c.payout_request_id = p.id), p.updated_at)) AS effective_updated_at
		FROM payout_requests p
		WHERE GREATEST(p.updated_at,
		          COALESCE((SELECT max(created_at) FROM payout_vendor_calls c WHERE c.payout_request_id = p.id), p.updated_at),
		          COALESCE((SELECT max(updated_at) FROM payout_vendor_commands c WHERE c.payout_request_id = p.id), p.updated_at)) <= $1
		  AND ($2::timestamptz IS NULL OR GREATEST(p.updated_at,
		          COALESCE((SELECT max(created_at) FROM payout_vendor_calls c WHERE c.payout_request_id = p.id), p.updated_at),
		          COALESCE((SELECT max(updated_at) FROM payout_vendor_commands c WHERE c.payout_request_id = p.id), p.updated_at)) > $2
		       OR (GREATEST(p.updated_at,
		          COALESCE((SELECT max(created_at) FROM payout_vendor_calls c WHERE c.payout_request_id = p.id), p.updated_at),
		          COALESCE((SELECT max(updated_at) FROM payout_vendor_commands c WHERE c.payout_request_id = p.id), p.updated_at)) = $2 AND p.id > $3))
		ORDER BY effective_updated_at ASC, p.id ASC LIMIT $4`
	args := []any{req.GetCutoff().AsTime(), nil, uuid.Nil, pageSize + 1}
	if !cursorTime.IsZero() {
		args[1], args[2] = cursorTime, cursorID
	}
	rows, err := m.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("payout assurance query: %w", err)
	}
	defer rows.Close()
	type rowValue struct {
		record    *payoutv1.AssuranceRecord
		effective time.Time
	}
	values := make([]rowValue, 0, pageSize)
	for rows.Next() {
		var id, userID uuid.UUID
		var created, effective time.Time
		var statusValue, currency, vendor, hold, settle, requestID string
		var quoteID, feeGateway string
		var amount, feeAmount int64
		var requestPresent bool
		var holdID, settleID sql.NullString
		if err := rows.Scan(&id, &created, &statusValue, &userID, &amount, &currency, &vendor,
			&holdID, &settleID, &requestID, &requestPresent, &quoteID, &feeAmount, &feeGateway, &effective); err != nil {
			return nil, fmt.Errorf("scan payout assurance record: %w", err)
		}
		hold, settle = holdID.String, settleID.String
		record := &payoutv1.AssuranceRecord{Id: id.String(), EffectiveUpdatedAt: timestamppb.New(effective),
			CreatedAt: timestamppb.New(created), Status: statusValue, UserId: userID.String(),
			Amount: fmt.Sprintf("%d", amount), Currency: currency, Vendor: vendor, HoldTxId: hold,
			SettleTxId: settle, RequestId: requestID, RequestIdPresent: requestPresent, FeeQuoteId: quoteID,
			FeeAmount: fmt.Sprintf("%d", feeAmount), FeeGateway: feeGateway}
		if err := m.populateAssuranceChildren(ctx, id, record); err != nil {
			return nil, err
		}
		values = append(values, rowValue{record: record, effective: effective})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate payout assurance records: %w", err)
	}
	hasMore := len(values) > pageSize
	if hasMore {
		values = values[:pageSize]
	}
	response := &payoutv1.ListAssuranceRecordsResponse{HasMore: hasMore, Records: make([]*payoutv1.AssuranceRecord, 0, len(values))}
	for _, value := range values {
		response.Records = append(response.Records, value.record)
	}
	if len(values) > 0 {
		response.NextId = values[len(values)-1].record.GetId()
		response.NextUpdatedAt = timestamppb.New(values[len(values)-1].effective)
	}
	return response, nil
}

func (m *Module) populateAssuranceChildren(ctx context.Context, id uuid.UUID, record *payoutv1.AssuranceRecord) error {
	calls, err := m.db.QueryContext(ctx, `SELECT attempt, COALESCE(NULLIF(substring(req_summary FROM 'vendor=([^ ]+)'), ''), '') AS vendor, outcome, created_at FROM payout_vendor_calls WHERE payout_request_id = $1 ORDER BY attempt, created_at`, id)
	if err != nil {
		return fmt.Errorf("payout assurance calls: %w", err)
	}
	for calls.Next() {
		var call payoutv1.VendorCallSummary
		var created time.Time
		if err := calls.Scan(&call.Attempt, &call.Vendor, &call.Outcome, &created); err != nil {
			calls.Close()
			return fmt.Errorf("scan payout assurance call: %w", err)
		}
		call.CreatedAt = timestamppb.New(created)
		record.VendorCalls = append(record.VendorCalls, &call)
	}
	if err := calls.Err(); err != nil {
		calls.Close()
		return fmt.Errorf("iterate payout assurance calls: %w", err)
	}
	calls.Close()
	commands, err := m.db.QueryContext(ctx, `SELECT id, vendor, attempt, status, updated_at FROM payout_vendor_commands WHERE payout_request_id = $1 ORDER BY attempt, id`, id)
	if err != nil {
		return fmt.Errorf("payout assurance commands: %w", err)
	}
	defer commands.Close()
	for commands.Next() {
		var command payoutv1.VendorCommandSummary
		var updated time.Time
		if err := commands.Scan(&command.Id, &command.Vendor, &command.Attempt, &command.Status, &updated); err != nil {
			return fmt.Errorf("scan payout assurance command: %w", err)
		}
		command.UpdatedAt = timestamppb.New(updated)
		record.VendorCommands = append(record.VendorCommands, &command)
	}
	return commands.Err()
}
