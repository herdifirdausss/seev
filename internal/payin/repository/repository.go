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
	"github.com/herdifirdausss/seev/pkg/cryptox"
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
	db   database.DatabaseSQL
	ring *cryptox.Ring
}

// NewRepository's ring is REQUIRED — docs/roadmap/archive/51-a8-data-lifecycle-privacy.md
// "A8 T2.5b" (the contract migration): payin_webhook_events.raw has no
// plaintext column anymore (migrations/payin/000010), so every write
// needs the ring to function at all. A NULL raw_ciphertext on an existing
// row is still a legitimate READ state (T2.6's own retention redaction
// nulls it after 30 days — see scanEvent below), which is why, unlike
// auth's own contract migration, the column itself stays nullable.
func NewRepository(db database.DatabaseSQL, ring *cryptox.Ring) Repository {
	if ring == nil {
		panic("payin: NewRepository requires a non-nil cryptox ring")
	}
	return &repo{db: db, ring: ring}
}

func rawAAD(eventID uuid.UUID) cryptox.AAD {
	return cryptox.AAD{Service: "payin", Table: "payin_webhook_events", Column: "raw", RowID: eventID.String()}
}

// redactedRawMarker is what scanEvent returns for a row T2.6's own
// retention redaction already nulled raw_ciphertext on — the exact same
// marker fn_retention_purge_webhook_events_raw used to write into the
// (now-dropped) plaintext raw column directly, kept for any caller that
// already expects to see it.
var redactedRawMarker = []byte(`{"redacted":true}`)

func (r *repo) GetOrInsert(ctx context.Context, ev model.WebhookEvent) (model.WebhookEvent, error) {
	rawCiphertext, err := r.ring.Seal(rawAAD(ev.ID), ev.Raw)
	if err != nil {
		return model.WebhookEvent{}, fmt.Errorf("encrypt payin webhook raw: %w", err)
	}
	v := r.ring.CurrentVersion()
	_, err = r.db.ExecContext(ctx, `
		INSERT INTO payin_webhook_events
			(id, vendor, vendor_event_id, external_ref, user_id, amount, currency, status, request_id, created_at, updated_at,
			 raw_ciphertext, raw_key_version)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'received', $8, now(), now(), $9, $10)`,
		ev.ID, ev.Vendor, ev.VendorEventID, ev.ExternalRef, ev.UserID, ev.Amount.IntPart(), ev.Currency,
		generalutil.NullString(ev.RequestID), rawCiphertext, v,
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
		SELECT id, vendor, vendor_event_id, external_ref, user_id, amount, currency, status,
		       COALESCE(error_message, ''), COALESCE(request_id, ''), created_at, updated_at,
		       raw_ciphertext
		FROM payin_webhook_events WHERE vendor = $1 AND vendor_event_id = $2`,
		vendor, vendorEventID)
	return r.scanEvent(row)
}

func (r *repo) Get(ctx context.Context, id uuid.UUID) (model.WebhookEvent, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, vendor, vendor_event_id, external_ref, user_id, amount, currency, status,
		       COALESCE(error_message, ''), COALESCE(request_id, ''), created_at, updated_at,
		       raw_ciphertext
		FROM payin_webhook_events WHERE id = $1`,
		id)
	ev, err := r.scanEvent(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.WebhookEvent{}, ErrNotFound
	}
	return ev, err
}

func (r *repo) scanEvent(scanner interface{ Scan(...any) error }) (model.WebhookEvent, error) {
	var ev model.WebhookEvent
	var amount int64
	var rawCiphertext []byte
	if err := scanner.Scan(&ev.ID, &ev.Vendor, &ev.VendorEventID, &ev.ExternalRef, &ev.UserID, &amount,
		&ev.Currency, &ev.Status, &ev.ErrorMessage, &ev.RequestID, &ev.CreatedAt, &ev.UpdatedAt,
		&rawCiphertext); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.WebhookEvent{}, err
		}
		return model.WebhookEvent{}, fmt.Errorf("scan payin webhook event: %w", err)
	}
	ev.Amount = decimal.NewFromInt(amount)
	if rawCiphertext == nil {
		// T2.6's own retention redaction already nulled this — nothing
		// left to decrypt, same marker the pre-contract plaintext column
		// used to carry.
		ev.Raw = redactedRawMarker
		return ev, nil
	}
	plain, err := r.ring.Open(rawAAD(ev.ID), rawCiphertext)
	if err != nil {
		return model.WebhookEvent{}, fmt.Errorf("decrypt payin webhook raw: %w", err)
	}
	ev.Raw = plain
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
	query := `SELECT id, vendor, vendor_event_id, external_ref, user_id, amount, currency, status,
	                 COALESCE(error_message, ''), COALESCE(request_id, ''), created_at, updated_at,
	                 raw_ciphertext
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
		ev, err := r.scanEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate payin webhook events: %w", err)
	}
	return out, nil
}
