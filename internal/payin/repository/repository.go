package repository

//go:generate mockgen -source=repository.go -destination=repository_mock.go -package=repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/herdifirdausss/seev/internal/payin/model"
	"github.com/herdifirdausss/seev/pkg/database"
	"github.com/herdifirdausss/seev/pkg/generalerror"
	"github.com/herdifirdausss/seev/pkg/generalutil"
)

// ErrNotFound is returned by Get when no row exists for the given id.
var ErrNotFound = errors.New("payin: webhook event not found")

// Repository persists payin webhook events (docs/roadmap/archive/22 Task T2).
type Repository interface {
	// GetOrInsert inserts a new 'received' row for
	// (ev.Vendor, ev.VendorEventID), or — if a row already exists for that
	// pair — returns the EXISTING row unchanged, ev is discarded. This is
	// the sole dedup mechanism (backed by the UNIQUE constraint, never a
	// read-then-write race): callers branch on the returned row's Status,
	// not on whether this call happened to insert or not (docs/roadmap/archive/22
	// Task T2 step 3 — the flow is identical either way).
	GetOrInsert(ctx context.Context, ev model.WebhookEvent) (model.WebhookEvent, error)

	MarkPosted(ctx context.Context, id uuid.UUID) error
	MarkFailed(ctx context.Context, id uuid.UUID, reason string) error
	// MarkBlocked records a fraud Block verdict (docs/roadmap/archive/37 Task T4) —
	// distinct from MarkFailed so an operator can tell "fraud rejected this
	// deposit" apart from "the ledger post itself failed" at a glance.
	MarkBlocked(ctx context.Context, id uuid.UUID, reason string) error

	Get(ctx context.Context, id uuid.UUID) (model.WebhookEvent, error)

	// List returns events newest first, optionally filtered by vendor
	// and/or status (both empty = no filter). Paginated.
	List(ctx context.Context, vendor, status string, limit, offset int) ([]model.WebhookEvent, error)

	// ─── Topup intents (docs/roadmap/archive/25 Task T3) ──────────────────────────

	InsertTopupIntent(ctx context.Context, intent model.TopupIntent) error
	GetTopupIntent(ctx context.Context, id uuid.UUID) (model.TopupIntent, error)
	// GetTopupIntentByReference reports found=false (not an error) when no
	// intent exists for reference — HandleWebhook falls back to the
	// payload's own user_id in that case (backward compatible).
	GetTopupIntentByReference(ctx context.Context, reference string) (intent model.TopupIntent, found bool, err error)
	// MarkTopupIntentSettled is a conditional UPDATE
	// (WHERE reference = $1 AND status = 'pending') — a safe no-op
	// (matched=false, no error) if the reference doesn't exist, is already
	// settled, or expired; the caller never needs to branch on matched,
	// this is fire-and-forget best-effort marking after a successful post.
	MarkTopupIntentSettled(ctx context.Context, reference string, eventID uuid.UUID) (matched bool, err error)
	// MarkTopupIntentExpired flips a lazily-discovered stale 'pending' row
	// (GetTopupIntent's own read path) to 'expired'.
	MarkTopupIntentExpired(ctx context.Context, id uuid.UUID) error
}

type repo struct {
	db database.DatabaseSQL
}

func NewRepository(db database.DatabaseSQL) Repository {
	return &repo{db: db}
}

func (r *repo) GetOrInsert(ctx context.Context, ev model.WebhookEvent) (model.WebhookEvent, error) {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO payin_webhook_events
			(id, vendor, vendor_event_id, external_ref, user_id, amount, currency, raw, status, request_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'received', $9, now(), now())`,
		ev.ID, ev.Vendor, ev.VendorEventID, ev.ExternalRef, ev.UserID, ev.Amount.IntPart(), ev.Currency, ev.Raw,
		generalutil.NullString(ev.RequestID),
	)
	if err != nil {
		if !generalerror.IsDuplicateKey(err) {
			return model.WebhookEvent{}, fmt.Errorf("insert payin webhook event: %w", err)
		}
		existing, getErr := r.getByVendorEventID(ctx, ev.Vendor, ev.VendorEventID)
		if getErr != nil {
			return model.WebhookEvent{}, fmt.Errorf("lookup existing payin webhook event: %w", getErr)
		}
		return existing, nil
	}
	ev.Status = "received"
	return ev, nil
}

func (r *repo) getByVendorEventID(ctx context.Context, vendor, vendorEventID string) (model.WebhookEvent, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, vendor, vendor_event_id, external_ref, user_id, amount, currency, raw, status,
		       COALESCE(error_message, ''), COALESCE(request_id, ''), created_at, updated_at
		FROM payin_webhook_events WHERE vendor = $1 AND vendor_event_id = $2`,
		vendor, vendorEventID)
	return scanEvent(row)
}

func (r *repo) Get(ctx context.Context, id uuid.UUID) (model.WebhookEvent, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, vendor, vendor_event_id, external_ref, user_id, amount, currency, raw, status,
		       COALESCE(error_message, ''), COALESCE(request_id, ''), created_at, updated_at
		FROM payin_webhook_events WHERE id = $1`,
		id)
	ev, err := scanEvent(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.WebhookEvent{}, ErrNotFound
	}
	return ev, err
}

func scanEvent(row *sql.Row) (model.WebhookEvent, error) {
	var ev model.WebhookEvent
	var amount int64
	if err := row.Scan(&ev.ID, &ev.Vendor, &ev.VendorEventID, &ev.ExternalRef, &ev.UserID, &amount,
		&ev.Currency, &ev.Raw, &ev.Status, &ev.ErrorMessage, &ev.RequestID, &ev.CreatedAt, &ev.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.WebhookEvent{}, err
		}
		return model.WebhookEvent{}, fmt.Errorf("scan payin webhook event: %w", err)
	}
	ev.Amount = decimal.NewFromInt(amount)
	return ev, nil
}

func (r *repo) MarkPosted(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE payin_webhook_events SET status = 'posted', updated_at = now() WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("mark payin webhook event posted: %w", err)
	}
	return nil
}

func (r *repo) MarkFailed(ctx context.Context, id uuid.UUID, reason string) error {
	if len(reason) > 500 {
		reason = reason[:500]
	}
	_, err := r.db.ExecContext(ctx, `
		UPDATE payin_webhook_events SET status = 'failed', error_message = $1, updated_at = now() WHERE id = $2`,
		reason, id)
	if err != nil {
		return fmt.Errorf("mark payin webhook event failed: %w", err)
	}
	return nil
}

func (r *repo) MarkBlocked(ctx context.Context, id uuid.UUID, reason string) error {
	if len(reason) > 500 {
		reason = reason[:500]
	}
	_, err := r.db.ExecContext(ctx, `
		UPDATE payin_webhook_events SET status = 'blocked', error_message = $1, updated_at = now() WHERE id = $2`,
		reason, id)
	if err != nil {
		return fmt.Errorf("mark payin webhook event blocked: %w", err)
	}
	return nil
}

func (r *repo) List(ctx context.Context, vendor, status string, limit, offset int) ([]model.WebhookEvent, error) {
	query := `SELECT id, vendor, vendor_event_id, external_ref, user_id, amount, currency, raw, status,
	                 COALESCE(error_message, ''), COALESCE(request_id, ''), created_at, updated_at
	          FROM payin_webhook_events WHERE 1=1`
	args := []any{}
	argN := 0
	if vendor != "" {
		argN++
		query += fmt.Sprintf(" AND vendor = $%d", argN)
		args = append(args, vendor)
	}
	if status != "" {
		argN++
		query += fmt.Sprintf(" AND status = $%d", argN)
		args = append(args, status)
	}
	query += fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d OFFSET $%d", argN+1, argN+2)
	args = append(args, limit, offset)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list payin webhook events: %w", err)
	}
	defer rows.Close()

	var out []model.WebhookEvent
	for rows.Next() {
		var ev model.WebhookEvent
		var amount int64
		if err := rows.Scan(&ev.ID, &ev.Vendor, &ev.VendorEventID, &ev.ExternalRef, &ev.UserID, &amount,
			&ev.Currency, &ev.Raw, &ev.Status, &ev.ErrorMessage, &ev.RequestID, &ev.CreatedAt, &ev.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan payin webhook event: %w", err)
		}
		ev.Amount = decimal.NewFromInt(amount)
		out = append(out, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate payin webhook events: %w", err)
	}
	return out, nil
}
