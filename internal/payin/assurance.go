package payin

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	payinv1 "github.com/herdifirdausss/seev/gen/payin/v1"
)

// ListAssuranceRecords is an owner-side, read-only projection. It deliberately
// selects only correlation fields; raw webhook JSON and vendor error bodies
// remain inside payin-service.
func (m *Module) ListAssuranceRecords(ctx context.Context, req *payinv1.ListAssuranceRecordsRequest) (*payinv1.ListAssuranceRecordsResponse, error) {
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
	cutoff := req.GetCutoff().AsTime()
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
		SELECT id, record_type, effective_updated_at, created_at, status, user_id,
		       amount, currency, vendor, reference, external_ref, settled_event_id,
		       request_id, request_id_present, ledger_type, ledger_gateway,
		       ledger_external_ref, ledger_idempotency_scope
			FROM (
			SELECT i.id, 'intent' AS record_type, i.updated_at AS effective_updated_at,
			       i.created_at, i.status, i.user_id, i.amount, i.currency, i.vendor,
			       i.reference, COALESCE(e.external_ref, ''), COALESCE(i.settled_event_id::text, ''),
			       COALESCE(i.request_id, ''), (i.request_id IS NOT NULL AND i.request_id <> ''),
			       CASE WHEN e.id IS NULL THEN '' ELSE 'money_in' END,
			       COALESCE(e.vendor, ''), COALESCE(e.external_ref, ''),
			       CASE WHEN e.id IS NULL THEN '' ELSE 'payin:' || e.vendor END
			FROM payin_topup_intents i
			LEFT JOIN LATERAL (
				SELECT e.*
				FROM payin_webhook_events e
				WHERE e.id = i.settled_event_id
				   OR (i.reference <> '' AND e.status = 'posted' AND e.user_id = i.user_id
				       AND e.amount = i.amount AND e.currency = i.currency AND e.external_ref = i.reference)
				ORDER BY (e.id = i.settled_event_id) DESC, e.updated_at DESC, e.id DESC
				LIMIT 1
			) e ON true
			UNION ALL
			SELECT e.id, 'webhook_event', e.updated_at, e.created_at, e.status, e.user_id,
			       e.amount, e.currency, e.vendor, '', e.external_ref, '',
			       COALESCE(e.request_id, ''), (e.request_id IS NOT NULL AND e.request_id <> ''),
			       'money_in', e.vendor, e.external_ref, 'payin:' || e.vendor
			FROM payin_webhook_events e
		) assurance
		WHERE effective_updated_at <= $1
		  AND ($2::timestamptz IS NULL OR effective_updated_at > $2 OR (effective_updated_at = $2 AND id > $3))
		ORDER BY effective_updated_at ASC, id ASC
		LIMIT $4`

	args := []any{cutoff, nil, uuid.Nil, pageSize + 1}
	if !cursorTime.IsZero() {
		args[1], args[2] = cursorTime, cursorID
	}
	rows, err := m.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("payin assurance query: %w", err)
	}
	defer rows.Close()
	records := make([]*payinv1.AssuranceRecord, 0, pageSize)
	for rows.Next() {
		var id, userID uuid.UUID
		var recordType, statusValue, currency, vendor, reference, externalRef, settledEventID string
		var requestID, ledgerType, ledgerGateway, ledgerExternalRef, ledgerScope string
		var effective, created time.Time
		var amount int64
		var requestPresent bool
		if err := rows.Scan(&id, &recordType, &effective, &created, &statusValue, &userID,
			&amount, &currency, &vendor, &reference, &externalRef, &settledEventID,
			&requestID, &requestPresent, &ledgerType, &ledgerGateway, &ledgerExternalRef,
			&ledgerScope); err != nil {
			return nil, fmt.Errorf("scan payin assurance record: %w", err)
		}
		records = append(records, &payinv1.AssuranceRecord{RecordType: recordType, Id: id.String(),
			EffectiveUpdatedAt: timestamppb.New(effective), CreatedAt: timestamppb.New(created),
			Status: statusValue, UserId: userID.String(), Amount: fmt.Sprintf("%d", amount), Currency: currency,
			Vendor: vendor, Reference: reference, ExternalRef: externalRef, SettledEventId: settledEventID,
			RequestId: requestID, RequestIdPresent: requestPresent, LedgerType: ledgerType,
			LedgerGateway: ledgerGateway, LedgerExternalRef: ledgerExternalRef,
			LedgerIdempotencyScope: ledgerScope})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate payin assurance records: %w", err)
	}
	hasMore := len(records) > pageSize
	if hasMore {
		records = records[:pageSize]
	}
	response := &payinv1.ListAssuranceRecordsResponse{Records: records, HasMore: hasMore}
	if len(records) > 0 {
		last := records[len(records)-1]
		response.NextId = last.GetId()
		response.NextUpdatedAt = last.GetEffectiveUpdatedAt()
	}
	return response, nil
}
