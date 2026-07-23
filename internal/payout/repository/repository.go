package repository

//go:generate mockgen -source=repository.go -destination=repository_mock.go -package=repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/herdifirdausss/seev/internal/payout/model"
	"github.com/herdifirdausss/seev/pkg/database"
	"github.com/herdifirdausss/seev/pkg/generalutil"
)

// ErrNotFound is returned by Get when no row exists for the given id.
var ErrNotFound = errors.New("payout: request not found")

// Repository persists payout requests and their vendor-call audit trail
// (docs/roadmap/archive/23 Task T1). Every state transition is an atomic conditional
// UPDATE (WHERE status = $from) — the sole concurrency-safety mechanism at
// this layer (a false return, nil error means "lost the race or already
// there", not a failure). The actual money-movement race protection is the
// LEDGER's own closed_by_tx_id guard (K3); these transitions are a read
// model reconciled after the fact, not an independent source of truth.
type Repository interface {
	Insert(ctx context.Context, req model.PayoutRequest) error

	// TransitionToHeld sets hold_tx_id and moves created->held.
	TransitionToHeld(ctx context.Context, id, holdTxID uuid.UUID) (bool, error)
	// TransitionToSubmitted moves held->submitted (about to call the vendor).
	TransitionToSubmitted(ctx context.Context, id uuid.UUID) (bool, error)
	// TransitionToVendorPending records vendorRef and moves submitted->vendor_pending.
	TransitionToVendorPending(ctx context.Context, id uuid.UUID, vendorRef string) (bool, error)
	// TransitionToSettled sets settle_tx_id and moves {submitted,vendor_pending}->settled.
	// The valid predecessor set is fixed here (not caller-supplied) — a
	// caller re-reading status before calling this and passing it back as
	// an expected "from" would be racing itself against a concurrent
	// transition; anchoring to the full valid set instead makes the
	// conditional UPDATE itself the single point of truth.
	TransitionToSettled(ctx context.Context, id, settleTxID uuid.UUID) (bool, error)
	// TransitionToCancelled sets settle_tx_id (the withdraw_cancel tx) and
	// moves {submitted,vendor_pending}->cancelled. Same fixed-predecessor-set
	// rationale as TransitionToSettled.
	TransitionToCancelled(ctx context.Context, id, settleTxID uuid.UUID) (bool, error)
	// TransitionToFailed records reason and moves {submitted,vendor_pending}->failed
	// (vendor said Failed, or the ledger permanently rejected the request).
	TransitionToFailed(ctx context.Context, id uuid.UUID, reason string) (bool, error)
	// TransitionBackToSubmitted resets vendor_pending->submitted for a retry
	// (e.g. admin-triggered re-Submit of a stuck request).
	TransitionBackToSubmitted(ctx context.Context, id uuid.UUID) (bool, error)
	// TransitionToRejected records reason and moves created->rejected
	// (docs/roadmap/archive/38 Task T5) — reached ONLY when a fee quote consumption
	// fails (expired/mismatch) at Create, before any hold was posted.
	TransitionToRejected(ctx context.Context, id uuid.UUID, reason string) (bool, error)

	// SetError records error_message WITHOUT changing status — infra
	// failures that leave status unchanged so the resume job retries.
	SetError(ctx context.Context, id uuid.UUID, reason string) error
	// SetQuotedFee records the fee LOCKED IN by a consumed quote
	// (docs/roadmap/archive/38 Task T5) — settle uses these stored values instead of
	// re-resolving fee_rules. Does not change status.
	SetQuotedFee(ctx context.Context, id, quoteID uuid.UUID, feeAmount decimal.Decimal, feeGateway string) error
	// SetVendor updates which vendor a request is currently routed to
	// (docs/roadmap/archive/40 Task T3's failover — a rejected vendor's replacement is
	// persisted here so the audit trail, admin UI, and any later resume
	// pass all see the CURRENT vendor). Does not change status.
	SetVendor(ctx context.Context, id uuid.UUID, vendor string) error

	Get(ctx context.Context, id uuid.UUID) (model.PayoutRequest, error)
	// List returns requests newest first, optionally filtered by status
	// and/or vendor (both empty = no filter).
	List(ctx context.Context, status, vendor string, limit, offset int) ([]model.PayoutRequest, error)
	// ListStuck returns requests in `status` whose updated_at is older
	// than olderThan — feeds the resume/polling job (docs/roadmap/archive/23 Task T3).
	ListStuck(ctx context.Context, status string, olderThan time.Time, limit int) ([]model.PayoutRequest, error)
	// CountStuck is ListStuck's unbounded sibling: ONE grouped-count query
	// across every status in statuses, whole backlog (no LIMIT) — feeds the
	// payout_stuck_requests gauge (docs/roadmap/archive/43 K5). Deliberately separate
	// from ListStuck: that method caps at 100 rows per resume pass, which
	// would silently under-report the gauge once the real backlog exceeds
	// that cap. The returned map omits any status with zero rows; the
	// caller fills 0 for those.
	//
	// Filters on created_at, NOT updated_at (docs/roadmap/archive/43 T5 regression —
	// an earlier draft copied ListStuck's updated_at filter here and the
	// gauge stayed at 0 through an entire live-fire test: the resume job's
	// OWN retry attempt touches updated_at every cron tick, so a request
	// that has been stuck for hours still looks "just touched" to an
	// updated_at filter and never crosses the threshold — a livelock
	// between the retry job's write and the gauge's read of the same
	// column. updated_at is the right column for ListStuck's job (retry
	// throttling: don't hammer the vendor more often than olderThan); this
	// gauge asks a different question — how long has the request existed
	// unresolved — which only created_at answers correctly.
	CountStuck(ctx context.Context, statuses []string, olderThan time.Time) (map[string]int, error)

	InsertVendorCall(ctx context.Context, call model.PayoutVendorCall) error
	// ListVendorCalls returns every vendor call ever recorded for a
	// request, oldest first — mayFailover (docs/roadmap/archive/40 Task T3) reads
	// this to decide whether any call has ever landed accepted/uncertain.
	ListVendorCalls(ctx context.Context, payoutRequestID uuid.UUID) ([]model.PayoutVendorCall, error)
}

type repo struct {
	db database.DatabaseSQL
}

func NewRepository(db database.DatabaseSQL) Repository {
	return &repo{db: db}
}

func (r *repo) Insert(ctx context.Context, req model.PayoutRequest) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO payout_requests
			(id, user_id, amount, currency, vendor, destination, status, created_by, request_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, 'created', $7, $8, now(), now())`,
		req.ID, req.UserID, req.Amount.IntPart(), req.Currency, req.Vendor, req.Destination, req.CreatedBy,
		generalutil.NullString(req.RequestID),
	)
	if err != nil {
		return fmt.Errorf("insert payout request: %w", err)
	}
	return nil
}

func (r *repo) transition(ctx context.Context, query string, args ...any) (bool, error) {
	res, err := r.db.ExecContext(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("transition payout request: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("transition payout request rows affected: %w", err)
	}
	return n > 0, nil
}

func (r *repo) TransitionToHeld(ctx context.Context, id, holdTxID uuid.UUID) (bool, error) {
	return r.transition(ctx, `
		UPDATE payout_requests SET status = 'held', hold_tx_id = $1, updated_at = now()
		WHERE id = $2 AND status = 'created'`,
		holdTxID, id)
}

func (r *repo) TransitionToSubmitted(ctx context.Context, id uuid.UUID) (bool, error) {
	return r.transition(ctx, `
		UPDATE payout_requests SET status = 'submitted', updated_at = now()
		WHERE id = $1 AND status IN ('held', 'vendor_pending')`,
		id)
}

func (r *repo) TransitionToVendorPending(ctx context.Context, id uuid.UUID, vendorRef string) (bool, error) {
	return r.transition(ctx, `
		UPDATE payout_requests SET status = 'vendor_pending', vendor_ref = $1, updated_at = now()
		WHERE id = $2 AND status = 'submitted'`,
		vendorRef, id)
}

func (r *repo) TransitionToSettled(ctx context.Context, id, settleTxID uuid.UUID) (bool, error) {
	return r.transition(ctx, `
		UPDATE payout_requests SET status = 'settled', settle_tx_id = $1, updated_at = now()
		WHERE id = $2 AND status IN ('submitted', 'vendor_pending')`,
		settleTxID, id)
}

func (r *repo) TransitionToCancelled(ctx context.Context, id, settleTxID uuid.UUID) (bool, error) {
	return r.transition(ctx, `
		UPDATE payout_requests SET status = 'cancelled', settle_tx_id = $1, updated_at = now()
		WHERE id = $2 AND status IN ('submitted', 'vendor_pending')`,
		settleTxID, id)
}

func (r *repo) TransitionToFailed(ctx context.Context, id uuid.UUID, reason string) (bool, error) {
	if len(reason) > 500 {
		reason = reason[:500]
	}
	return r.transition(ctx, `
		UPDATE payout_requests SET status = 'failed', error_message = $1, updated_at = now()
		WHERE id = $2 AND status IN ('submitted', 'vendor_pending')`,
		reason, id)
}

func (r *repo) TransitionBackToSubmitted(ctx context.Context, id uuid.UUID) (bool, error) {
	return r.transition(ctx, `
		UPDATE payout_requests SET status = 'submitted', updated_at = now()
		WHERE id = $1 AND status = 'vendor_pending'`,
		id)
}

func (r *repo) TransitionToRejected(ctx context.Context, id uuid.UUID, reason string) (bool, error) {
	if len(reason) > 500 {
		reason = reason[:500]
	}
	return r.transition(ctx, `
		UPDATE payout_requests SET status = 'rejected', error_message = $1, updated_at = now()
		WHERE id = $2 AND status = 'created'`,
		reason, id)
}

func (r *repo) SetError(ctx context.Context, id uuid.UUID, reason string) error {
	if len(reason) > 500 {
		reason = reason[:500]
	}
	_, err := r.db.ExecContext(ctx, `
		UPDATE payout_requests SET error_message = $1, updated_at = now() WHERE id = $2`,
		reason, id)
	if err != nil {
		return fmt.Errorf("set payout request error: %w", err)
	}
	return nil
}

func (r *repo) SetQuotedFee(ctx context.Context, id, quoteID uuid.UUID, feeAmount decimal.Decimal, feeGateway string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE payout_requests SET fee_quote_id = $1, fee_amount = $2, fee_gateway = $3, updated_at = now() WHERE id = $4`,
		quoteID, feeAmount.IntPart(), feeGateway, id)
	if err != nil {
		return fmt.Errorf("set payout request quoted fee: %w", err)
	}
	return nil
}

func (r *repo) SetVendor(ctx context.Context, id uuid.UUID, vendor string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE payout_requests SET vendor = $1, updated_at = now() WHERE id = $2`,
		vendor, id)
	if err != nil {
		return fmt.Errorf("set payout request vendor: %w", err)
	}
	return nil
}

func (r *repo) Get(ctx context.Context, id uuid.UUID) (model.PayoutRequest, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, user_id, amount, currency, vendor, destination, status, hold_tx_id, settle_tx_id,
		       COALESCE(vendor_ref, ''), COALESCE(error_message, ''), created_by, COALESCE(request_id, ''),
		       fee_quote_id, fee_amount, COALESCE(fee_gateway, ''), created_at, updated_at
		FROM payout_requests WHERE id = $1`, id)
	req, err := scanRequest(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.PayoutRequest{}, ErrNotFound
	}
	return req, err
}

// rowScanner is the subset both *sql.Row and *sql.Rows satisfy — lets
// scanRequest serve both Get (single row) and the list queries (multiple
// rows) without duplicating the column list/nullable-UUID handling.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanRequest scans one payout_requests row. hold_tx_id/settle_tx_id are
// nullable UUID columns — scanned via sql.NullString then parsed, the same
// established pattern as internal/ledger/repository's closed_by_tx_id
// (uuid.UUID's Scan method doesn't reliably handle a NULL through a bare
// **uuid.UUID destination).
func scanRequest(s rowScanner) (model.PayoutRequest, error) {
	var req model.PayoutRequest
	var amount int64
	var holdTxID, settleTxID, feeQuoteID sql.NullString
	var feeAmount sql.NullInt64
	if err := s.Scan(&req.ID, &req.UserID, &amount, &req.Currency, &req.Vendor, &req.Destination, &req.Status,
		&holdTxID, &settleTxID, &req.VendorRef, &req.ErrorMessage, &req.CreatedBy, &req.RequestID,
		&feeQuoteID, &feeAmount, &req.FeeGateway,
		&req.CreatedAt, &req.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.PayoutRequest{}, err
		}
		return model.PayoutRequest{}, fmt.Errorf("scan payout request: %w", err)
	}
	req.Amount = decimal.NewFromInt(amount)
	if holdTxID.Valid {
		id, err := uuid.Parse(holdTxID.String)
		if err != nil {
			return model.PayoutRequest{}, fmt.Errorf("scan payout request: invalid stored hold_tx_id: %w", err)
		}
		req.HoldTxID = &id
	}
	if settleTxID.Valid {
		id, err := uuid.Parse(settleTxID.String)
		if err != nil {
			return model.PayoutRequest{}, fmt.Errorf("scan payout request: invalid stored settle_tx_id: %w", err)
		}
		req.SettleTxID = &id
	}
	if feeQuoteID.Valid {
		id, err := uuid.Parse(feeQuoteID.String)
		if err != nil {
			return model.PayoutRequest{}, fmt.Errorf("scan payout request: invalid stored fee_quote_id: %w", err)
		}
		req.FeeQuoteID = &id
	}
	if feeAmount.Valid {
		fee := decimal.NewFromInt(feeAmount.Int64)
		req.FeeAmount = &fee
	}
	return req, nil
}

func (r *repo) List(ctx context.Context, status, vendor string, limit, offset int) ([]model.PayoutRequest, error) {
	query := `SELECT id, user_id, amount, currency, vendor, destination, status, hold_tx_id, settle_tx_id,
	                 COALESCE(vendor_ref, ''), COALESCE(error_message, ''), created_by, COALESCE(request_id, ''),
	                 fee_quote_id, fee_amount, COALESCE(fee_gateway, ''), created_at, updated_at
	          FROM payout_requests WHERE 1=1`
	args := []any{}
	argN := 0
	if status != "" {
		argN++
		query += fmt.Sprintf(" AND status = $%d", argN)
		args = append(args, status)
	}
	if vendor != "" {
		argN++
		query += fmt.Sprintf(" AND vendor = $%d", argN)
		args = append(args, vendor)
	}
	query += fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d OFFSET $%d", argN+1, argN+2)
	args = append(args, limit, offset)

	return r.queryRequests(ctx, query, args...)
}

func (r *repo) ListStuck(ctx context.Context, status string, olderThan time.Time, limit int) ([]model.PayoutRequest, error) {
	return r.queryRequests(ctx, `
		SELECT id, user_id, amount, currency, vendor, destination, status, hold_tx_id, settle_tx_id,
		       COALESCE(vendor_ref, ''), COALESCE(error_message, ''), created_by, COALESCE(request_id, ''),
		       fee_quote_id, fee_amount, COALESCE(fee_gateway, ''), created_at, updated_at
		FROM payout_requests
		WHERE status = $1 AND updated_at < $2
		ORDER BY updated_at
		LIMIT $3`,
		status, olderThan, limit)
}

func (r *repo) CountStuck(ctx context.Context, statuses []string, olderThan time.Time) (map[string]int, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT status, COUNT(*)
		FROM payout_requests
		WHERE status = ANY($1) AND created_at < $2
		GROUP BY status`,
		statuses, olderThan)
	if err != nil {
		return nil, fmt.Errorf("count stuck payout requests: %w", err)
	}
	defer rows.Close()

	counts := make(map[string]int, len(statuses))
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, fmt.Errorf("scan stuck count: %w", err)
		}
		counts[status] = count
	}
	return counts, rows.Err()
}

func (r *repo) queryRequests(ctx context.Context, query string, args ...any) ([]model.PayoutRequest, error) {
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query payout requests: %w", err)
	}
	defer rows.Close()

	var out []model.PayoutRequest
	for rows.Next() {
		req, err := scanRequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, req)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate payout requests: %w", err)
	}
	return out, nil
}

func (r *repo) InsertVendorCall(ctx context.Context, call model.PayoutVendorCall) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO payout_vendor_calls (id, payout_request_id, attempt, req_summary, resp_status, error, outcome, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, now())`,
		call.ID, call.PayoutRequestID, call.Attempt, call.ReqSummary, nullString(call.RespStatus), nullString(call.Error), call.Outcome,
	)
	if err != nil {
		return fmt.Errorf("insert payout vendor call: %w", err)
	}
	return nil
}

func (r *repo) ListVendorCalls(ctx context.Context, payoutRequestID uuid.UUID) ([]model.PayoutVendorCall, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, payout_request_id, attempt, req_summary, COALESCE(resp_status, ''), COALESCE(error, ''), outcome, created_at
		FROM payout_vendor_calls WHERE payout_request_id = $1 ORDER BY created_at ASC`, payoutRequestID)
	if err != nil {
		return nil, fmt.Errorf("list payout vendor calls: %w", err)
	}
	defer rows.Close()
	var out []model.PayoutVendorCall
	for rows.Next() {
		var c model.PayoutVendorCall
		if err := rows.Scan(&c.ID, &c.PayoutRequestID, &c.Attempt, &c.ReqSummary, &c.RespStatus, &c.Error, &c.Outcome, &c.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan payout vendor call: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
