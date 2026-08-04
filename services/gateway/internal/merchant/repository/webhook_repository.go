package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/herdifirdausss/seev/internal/platform/database"
	"github.com/herdifirdausss/seev/services/gateway/internal/merchant/model"
)

type webhookRepository struct {
	db database.DatabaseSQL
}

func (r *webhookRepository) CreateEndpoint(ctx context.Context, e model.WebhookEndpoint) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO merchant_webhook_endpoints
			(id, public_id, tenant_id, url, status, secret_ciphertext, secret_version, subscribed_events, environment, description, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, now(), now())`,
		e.ID, e.PublicID, e.TenantID, e.URL, e.Status, e.SecretCiphertext, e.SecretVersion,
		e.SubscribedEvents, e.Environment, e.Description,
	)
	if err != nil {
		return fmt.Errorf("merchant: create webhook endpoint: %w", err)
	}
	return nil
}

func (r *webhookRepository) GetEndpoint(ctx context.Context, tenantID, endpointID uuid.UUID) (model.WebhookEndpoint, error) {
	return r.scanEndpoint(r.db.QueryRowContext(ctx, `
		SELECT id, public_id, tenant_id, url, status, secret_ciphertext, secret_version, subscribed_events,
		       environment, description, created_at, updated_at, disabled_at
		FROM merchant_webhook_endpoints WHERE tenant_id = $1 AND id = $2`, tenantID, endpointID))
}

func (r *webhookRepository) ListEndpoints(ctx context.Context, tenantID uuid.UUID) ([]model.WebhookEndpoint, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, public_id, tenant_id, url, status, secret_ciphertext, secret_version, subscribed_events,
		       environment, description, created_at, updated_at, disabled_at
		FROM merchant_webhook_endpoints WHERE tenant_id = $1 ORDER BY created_at DESC`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("merchant: list webhook endpoints: %w", err)
	}
	defer rows.Close()

	var out []model.WebhookEndpoint
	for rows.Next() {
		e, err := r.scanEndpointRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (r *webhookRepository) UpdateEndpoint(ctx context.Context, e model.WebhookEndpoint) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE merchant_webhook_endpoints
		SET url = $1, status = $2, subscribed_events = $3, description = $4, updated_at = now(),
		    disabled_at = CASE WHEN $2 = 'disabled' AND status <> 'disabled' THEN now() ELSE disabled_at END
		WHERE tenant_id = $5 AND id = $6`,
		e.URL, e.Status, e.SubscribedEvents, e.Description, e.TenantID, e.ID,
	)
	if err != nil {
		return fmt.Errorf("merchant: update webhook endpoint: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *webhookRepository) DeleteEndpoint(ctx context.Context, tenantID, endpointID uuid.UUID) error {
	res, err := r.db.ExecContext(ctx, `
		DELETE FROM merchant_webhook_endpoints WHERE tenant_id = $1 AND id = $2`, tenantID, endpointID)
	if err != nil {
		return fmt.Errorf("merchant: delete webhook endpoint: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DisableEndpoint is called by the T7 relay worker on a 410 response — not
// tenant-scoped by an external caller, since it is an internal system
// action on an endpoint the worker already resolved.
func (r *webhookRepository) DisableEndpoint(ctx context.Context, endpointID uuid.UUID) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE merchant_webhook_endpoints SET status = 'disabled', disabled_at = now(), updated_at = now()
		WHERE id = $1 AND status <> 'disabled'`, endpointID)
	if err != nil {
		return fmt.Errorf("merchant: disable webhook endpoint: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *webhookRepository) CreateEvent(ctx context.Context, e model.WebhookEvent) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO merchant_webhook_events
			(id, public_id, tenant_id, event_type, schema_version, livemode, payload, payload_bytes, source_event_id, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, now())`,
		e.ID, e.PublicID, e.TenantID, e.EventType, e.SchemaVersion, e.Livemode, e.Payload, e.PayloadBytes, e.SourceEventID,
	)
	if err != nil {
		return fmt.Errorf("merchant: create webhook event: %w", err)
	}
	return nil
}

func (r *webhookRepository) GetEventBySource(ctx context.Context, tenantID, sourceEventID uuid.UUID, eventType string) (model.WebhookEvent, bool, error) {
	var e model.WebhookEvent
	err := r.db.QueryRowContext(ctx, `
		SELECT id, public_id, tenant_id, event_type, schema_version, livemode, payload, payload_bytes, source_event_id, created_at
		FROM merchant_webhook_events WHERE tenant_id = $1 AND source_event_id = $2 AND event_type = $3`,
		tenantID, sourceEventID, eventType,
	).Scan(&e.ID, &e.PublicID, &e.TenantID, &e.EventType, &e.SchemaVersion, &e.Livemode, &e.Payload, &e.PayloadBytes,
		&e.SourceEventID, &e.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return model.WebhookEvent{}, false, nil
	}
	if err != nil {
		return model.WebhookEvent{}, false, fmt.Errorf("merchant: get webhook event by source: %w", err)
	}
	return e, true, nil
}

func (r *webhookRepository) GetEventByID(ctx context.Context, eventID uuid.UUID) (model.WebhookEvent, error) {
	var e model.WebhookEvent
	err := r.db.QueryRowContext(ctx, `
		SELECT id, public_id, tenant_id, event_type, schema_version, livemode, payload, payload_bytes, source_event_id, created_at
		FROM merchant_webhook_events WHERE id = $1`,
		eventID,
	).Scan(&e.ID, &e.PublicID, &e.TenantID, &e.EventType, &e.SchemaVersion, &e.Livemode, &e.Payload, &e.PayloadBytes,
		&e.SourceEventID, &e.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return model.WebhookEvent{}, ErrNotFound
	}
	if err != nil {
		return model.WebhookEvent{}, fmt.Errorf("merchant: get webhook event by id: %w", err)
	}
	return e, nil
}

func (r *webhookRepository) CreateDelivery(ctx context.Context, d model.WebhookDelivery) (bool, error) {
	res, err := r.db.ExecContext(ctx, `
		INSERT INTO merchant_webhook_deliveries
			(id, public_id, tenant_id, endpoint_id, event_id, status, attempt_count, next_attempt_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now(), now())
		ON CONFLICT (endpoint_id, event_id) WHERE replay_of_delivery_id IS NULL DO NOTHING`,
		d.ID, d.PublicID, d.TenantID, d.EndpointID, d.EventID, d.Status, d.AttemptCount, d.NextAttemptAt,
	)
	if err != nil {
		return false, fmt.Errorf("merchant: create webhook delivery: %w", err)
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// CreateReplayDelivery inserts a NEW delivery row sharing the same
// EventID as an earlier one (T7 "replay creates a new delivery ID with
// the same event ID"). d.ReplayOfDeliveryID must be set — that is what
// exempts this row from the automatic path's partial unique index
// (idx_merchant_webhook_deliveries_automatic_unique WHERE
// replay_of_delivery_id IS NULL), not a bare plain-INSERT assumption (an
// earlier draft of this migration got that wrong; caught by this
// package's own integration test).
func (r *webhookRepository) CreateReplayDelivery(ctx context.Context, d model.WebhookDelivery) error {
	if d.ReplayOfDeliveryID == nil {
		return fmt.Errorf("merchant: create replay webhook delivery: ReplayOfDeliveryID is required")
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO merchant_webhook_deliveries
			(id, public_id, tenant_id, endpoint_id, event_id, replay_of_delivery_id, status, attempt_count, next_attempt_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, now(), now())`,
		d.ID, d.PublicID, d.TenantID, d.EndpointID, d.EventID, d.ReplayOfDeliveryID, d.Status, d.AttemptCount, d.NextAttemptAt,
	)
	if err != nil {
		return fmt.Errorf("merchant: create replay webhook delivery: %w", err)
	}
	return nil
}

func (r *webhookRepository) GetDelivery(ctx context.Context, tenantID, deliveryID uuid.UUID) (model.WebhookDelivery, error) {
	return r.scanDelivery(r.db.QueryRowContext(ctx, `
		SELECT id, public_id, tenant_id, endpoint_id, event_id, replay_of_delivery_id, status, attempt_count, next_attempt_at,
		       lease_owner, lease_expires_at, last_http_status, last_error_code, first_attempt_at,
		       delivered_at, dead_at, created_at, updated_at
		FROM merchant_webhook_deliveries WHERE tenant_id = $1 AND id = $2`, tenantID, deliveryID))
}

func (r *webhookRepository) ListDeliveries(ctx context.Context, tenantID uuid.UUID, limit int) ([]model.WebhookDelivery, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, public_id, tenant_id, endpoint_id, event_id, replay_of_delivery_id, status, attempt_count, next_attempt_at,
		       lease_owner, lease_expires_at, last_http_status, last_error_code, first_attempt_at,
		       delivered_at, dead_at, created_at, updated_at
		FROM merchant_webhook_deliveries WHERE tenant_id = $1 ORDER BY created_at DESC LIMIT $2`, tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("merchant: list webhook deliveries: %w", err)
	}
	defer rows.Close()

	var out []model.WebhookDelivery
	for rows.Next() {
		d, err := r.scanDeliveryRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ListDue is the T7 relay worker's own claim query — not tenant-scoped,
// since the worker processes every tenant's due deliveries in one pass.
func (r *webhookRepository) ListDue(ctx context.Context, limit int) ([]model.WebhookDelivery, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, public_id, tenant_id, endpoint_id, event_id, replay_of_delivery_id, status, attempt_count, next_attempt_at,
		       lease_owner, lease_expires_at, last_http_status, last_error_code, first_attempt_at,
		       delivered_at, dead_at, created_at, updated_at
		FROM merchant_webhook_deliveries
		WHERE status IN ('pending', 'failed') AND (next_attempt_at IS NULL OR next_attempt_at <= now())
		ORDER BY created_at
		LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("merchant: list due webhook deliveries: %w", err)
	}
	defer rows.Close()

	var out []model.WebhookDelivery
	for rows.Next() {
		d, err := r.scanDeliveryRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ClaimDue atomically assigns a batch of due deliveries to leaseOwner —
// same WHERE-clause-as-compare-and-swap + FOR UPDATE SKIP LOCKED shape
// internal/platform/lifecycle/objectoutbox.Worker's own claimPending already established for this
// exact "durable outbox" pattern (docs/reference/c1-current-contract-inventory.md
// §5 flagged it as directly reusable for T7). A row whose lease already
// expired (lease_expires_at < now()) is just as claimable as one that was
// never leased at all — that symmetry is what recovers a crashed worker's
// dangling lease on the very next poll.
func (r *webhookRepository) ClaimDue(ctx context.Context, limit int, leaseOwner string, leaseExpiresAt time.Time) ([]model.WebhookDelivery, error) {
	rows, err := r.db.QueryContext(ctx, `
		WITH claimed AS (
			UPDATE merchant_webhook_deliveries
			SET lease_owner = $1, lease_expires_at = $2, updated_at = now()
			WHERE id IN (
				SELECT id FROM merchant_webhook_deliveries
				WHERE status IN ('pending', 'failed')
				  AND (next_attempt_at IS NULL OR next_attempt_at <= now())
				  AND (lease_owner IS NULL OR lease_expires_at < now())
				ORDER BY created_at
				LIMIT $3
				FOR UPDATE SKIP LOCKED
			)
			RETURNING id, public_id, tenant_id, endpoint_id, event_id, replay_of_delivery_id, status, attempt_count,
			          next_attempt_at, lease_owner, lease_expires_at, last_http_status, last_error_code,
			          first_attempt_at, delivered_at, dead_at, created_at, updated_at
		)
		SELECT id, public_id, tenant_id, endpoint_id, event_id, replay_of_delivery_id, status, attempt_count,
		       next_attempt_at, lease_owner, lease_expires_at, last_http_status, last_error_code,
		       first_attempt_at, delivered_at, dead_at, created_at, updated_at
		FROM claimed`,
		leaseOwner, leaseExpiresAt, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("merchant: claim due webhook deliveries: %w", err)
	}
	defer rows.Close()

	var out []model.WebhookDelivery
	for rows.Next() {
		d, err := r.scanDeliveryRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (r *webhookRepository) MarkDelivered(ctx context.Context, deliveryID uuid.UUID, httpStatus int) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE merchant_webhook_deliveries
		SET status = 'delivered', last_http_status = $1, delivered_at = now(), attempt_count = attempt_count + 1,
		    lease_owner = NULL, lease_expires_at = NULL, updated_at = now()
		WHERE id = $2`, httpStatus, deliveryID)
	if err != nil {
		return fmt.Errorf("merchant: mark webhook delivery delivered: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *webhookRepository) MarkFailedAttempt(ctx context.Context, deliveryID uuid.UUID, errorCode string, httpStatus *int, nextAttemptAt any) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE merchant_webhook_deliveries
		SET status = 'failed', last_error_code = $1, last_http_status = $2, next_attempt_at = $3,
		    attempt_count = attempt_count + 1, first_attempt_at = COALESCE(first_attempt_at, now()),
		    lease_owner = NULL, lease_expires_at = NULL, updated_at = now()
		WHERE id = $4`, errorCode, httpStatus, nextAttemptAt, deliveryID)
	if err != nil {
		return fmt.Errorf("merchant: mark webhook delivery failed attempt: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *webhookRepository) MarkDead(ctx context.Context, deliveryID uuid.UUID) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE merchant_webhook_deliveries
		SET status = 'dead', dead_at = now(), lease_owner = NULL, lease_expires_at = NULL, updated_at = now()
		WHERE id = $1`, deliveryID)
	if err != nil {
		return fmt.Errorf("merchant: mark webhook delivery dead: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *webhookRepository) RecordAttempt(ctx context.Context, a model.WebhookAttempt) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO merchant_webhook_attempts
			(id, delivery_id, attempt_number, started_at, finished_at, http_status, duration_ms, error_code, response_excerpt)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		a.ID, a.DeliveryID, a.AttemptNumber, a.StartedAt, a.FinishedAt, a.HTTPStatus, a.DurationMS, a.ErrorCode, a.ResponseExcerpt,
	)
	if err != nil {
		return fmt.Errorf("merchant: record webhook attempt: %w", err)
	}
	return nil
}

func (r *webhookRepository) BacklogStats(ctx context.Context) (map[string]int, *time.Time, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT status, count(*) FROM merchant_webhook_deliveries GROUP BY status`)
	if err != nil {
		return nil, nil, fmt.Errorf("merchant: webhook delivery status counts: %w", err)
	}
	counts := map[string]int{}
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			rows.Close()
			return nil, nil, fmt.Errorf("merchant: scan webhook delivery status count: %w", err)
		}
		counts[status] = count
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, nil, err
	}
	rows.Close()

	var oldestPendingAt sql.NullTime
	err = r.db.QueryRowContext(ctx, `
		SELECT min(created_at) FROM merchant_webhook_deliveries WHERE status IN ('pending', 'failed')`).Scan(&oldestPendingAt)
	if err != nil {
		return nil, nil, fmt.Errorf("merchant: oldest pending webhook delivery: %w", err)
	}
	if !oldestPendingAt.Valid {
		return counts, nil, nil
	}
	return counts, &oldestPendingAt.Time, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func (r *webhookRepository) scanEndpoint(row *sql.Row) (model.WebhookEndpoint, error) {
	e, err := scanEndpointFrom(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.WebhookEndpoint{}, ErrNotFound
	}
	if err != nil {
		return model.WebhookEndpoint{}, fmt.Errorf("merchant: scan webhook endpoint: %w", err)
	}
	return e, nil
}

func (r *webhookRepository) scanEndpointRow(row rowScanner) (model.WebhookEndpoint, error) {
	e, err := scanEndpointFrom(row)
	if err != nil {
		return model.WebhookEndpoint{}, fmt.Errorf("merchant: scan webhook endpoint: %w", err)
	}
	return e, nil
}

// pgTypeMap backs scanEndpointFrom's subscribed_events decoding. Plain
// database/sql.Scan does not know how to decode a Postgres TEXT[] into a
// Go []string on its own (confirmed by a live integration test failure:
// "unsupported Scan, storing driver.Value type string into type
// *[]string") — pgtype.Map.SQLScanner is pgx's own documented bridge for
// exactly this case ("necessary for types like Array[T] ... where the
// type needs assistance from Map to implement the sql.Scanner interface").
var pgTypeMap = pgtype.NewMap()

func scanEndpointFrom(row rowScanner) (model.WebhookEndpoint, error) {
	var e model.WebhookEndpoint
	err := row.Scan(&e.ID, &e.PublicID, &e.TenantID, &e.URL, &e.Status, &e.SecretCiphertext, &e.SecretVersion,
		pgTypeMap.SQLScanner(&e.SubscribedEvents), &e.Environment, &e.Description, &e.CreatedAt, &e.UpdatedAt, &e.DisabledAt)
	return e, err
}

func (r *webhookRepository) scanDelivery(row *sql.Row) (model.WebhookDelivery, error) {
	d, err := scanDeliveryFrom(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.WebhookDelivery{}, ErrNotFound
	}
	if err != nil {
		return model.WebhookDelivery{}, fmt.Errorf("merchant: scan webhook delivery: %w", err)
	}
	return d, nil
}

func (r *webhookRepository) scanDeliveryRow(row rowScanner) (model.WebhookDelivery, error) {
	d, err := scanDeliveryFrom(row)
	if err != nil {
		return model.WebhookDelivery{}, fmt.Errorf("merchant: scan webhook delivery: %w", err)
	}
	return d, nil
}

func scanDeliveryFrom(row rowScanner) (model.WebhookDelivery, error) {
	var d model.WebhookDelivery
	err := row.Scan(&d.ID, &d.PublicID, &d.TenantID, &d.EndpointID, &d.EventID, &d.ReplayOfDeliveryID, &d.Status, &d.AttemptCount,
		&d.NextAttemptAt, &d.LeaseOwner, &d.LeaseExpiresAt, &d.LastHTTPStatus, &d.LastErrorCode,
		&d.FirstAttemptAt, &d.DeliveredAt, &d.DeadAt, &d.CreatedAt, &d.UpdatedAt)
	return d, err
}
