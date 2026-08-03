package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/notify/model"
)

func (r *platformRepo) AddDigestItem(ctx context.Context, request model.DigestRequest) error {
	return r.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		return insertDigestItemTx(ctx, tx, request)
	})
}

func insertDigestItemTx(ctx context.Context, tx *sql.Tx, request model.DigestRequest) error {
	var windowID uuid.UUID
	locale := request.Locale
	if locale == "" {
		locale = "en-US"
	}
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO notif_digest_windows
			(id,user_id,channel,locale,timezone,local_window_date,window_start_at,window_end_at,scheduled_at,status)
		VALUES($1,$2,'email',$3,$4,$5,$6,$7,$8,'scheduled')
		ON CONFLICT(user_id,channel,local_window_date,timezone)
		DO UPDATE SET updated_at=now()
		RETURNING id`,
		uuid.New(), request.UserID, locale, request.Timezone, request.LocalDate,
		request.WindowStart, request.WindowEnd, request.ScheduledAt).Scan(&windowID); err != nil {
		return fmt.Errorf("create digest window: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO notif_digest_items(digest_window_id,notification_id)
		VALUES($1,$2) ON CONFLICT(digest_window_id,notification_id) DO NOTHING`, windowID, request.NotificationID); err != nil {
		return fmt.Errorf("insert digest item: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE notif_digest_windows
		SET item_count=(SELECT count(*) FROM notif_digest_items WHERE digest_window_id=$1),updated_at=now()
		WHERE id=$1`, windowID); err != nil {
		return err
	}
	return nil
}

func (r *platformRepo) ClaimDigestWindows(ctx context.Context, limit int, owner string, leaseUntil time.Time) ([]model.DigestWindow, error) {
	rows, err := r.db.QueryContext(ctx, `WITH claimed AS (SELECT id FROM notif_digest_windows WHERE status IN ('scheduled','processing') AND scheduled_at<=now() AND (lease_expires_at IS NULL OR lease_expires_at<now()) ORDER BY scheduled_at,id LIMIT $1 FOR UPDATE SKIP LOCKED) UPDATE notif_digest_windows d SET status='processing',lease_owner=$2,lease_expires_at=$3,updated_at=now() FROM claimed WHERE d.id=claimed.id RETURNING d.id,d.user_id,d.channel,d.locale,d.timezone,d.local_window_date,d.window_start_at,d.window_end_at,d.scheduled_at,d.status,d.item_count,d.delivery_id,d.lease_owner,d.lease_expires_at`, limit, owner, leaseUntil)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.DigestWindow
	for rows.Next() {
		var w model.DigestWindow
		var deliveryID uuid.NullUUID
		if err := rows.Scan(&w.ID, &w.UserID, &w.Channel, &w.Locale, &w.Timezone, &w.LocalWindowDate, &w.WindowStartAt, &w.WindowEndAt, &w.ScheduledAt, &w.Status, &w.ItemCount, &deliveryID, &w.LeaseOwner, &w.LeaseExpiresAt); err != nil {
			return nil, err
		}
		if deliveryID.Valid {
			w.DeliveryID = &deliveryID.UUID
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func (r *platformRepo) ListDigestNotifications(ctx context.Context, windowID, userID uuid.UUID) ([]model.Notification, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT n.id,n.user_id,n.event_id,n.type,n.title,n.body,n.payload,n.read_at,n.created_at,n.event_type,n.source_service,n.kind,n.category,n.priority,n.requirement,n.locale,n.template_version_id,COALESCE(n.deep_link,''),n.context,n.content_hash,n.expires_at,n.updated_at FROM notif_digest_items i JOIN notif_notifications n ON n.id=i.notification_id JOIN notif_digest_windows w ON w.id=i.digest_window_id WHERE i.digest_window_id=$1 AND n.user_id=$2 AND n.created_at >= w.window_start_at AND n.created_at < w.window_end_at AND (n.read_at IS NULL OR n.requirement='mandatory') ORDER BY n.created_at,n.id LIMIT 101`, windowID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Notification
	for rows.Next() {
		n, err := scanModernNotification(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (r *platformRepo) CreateDigestDelivery(ctx context.Context, w model.DigestWindow, subject, text, html string, templateID uuid.UUID, hash []byte) error {
	deliveryID := uuid.New()
	locale := w.Locale
	if locale == "" {
		locale = "en-US"
	}
	var persistedID uuid.UUID
	err := r.db.QueryRowContext(ctx, `
		INSERT INTO notif_deliveries
			(id,digest_window_id,user_id,channel,endpoint_identity,status,template_version_id,locale,
			rendered_subject,rendered_text,rendered_html,content_hash,next_attempt_at)
		VALUES($1,$2,$3,'email','digest','pending_recipient',$4,$5,$6,$7,$8,$9,now())
		ON CONFLICT (digest_window_id,channel) WHERE digest_window_id IS NOT NULL
		DO UPDATE SET updated_at=now()
		RETURNING id`, deliveryID, w.ID, w.UserID, templateID, locale, subject, text, html, hash).Scan(&persistedID)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `UPDATE notif_digest_windows SET delivery_id=$1,updated_at=now() WHERE id=$2`, persistedID, w.ID)
	return err
}
func (r *platformRepo) FinishDigestWindow(ctx context.Context, windowID uuid.UUID, owner, status string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE notif_digest_windows SET status=$3,lease_owner=NULL,lease_expires_at=NULL,updated_at=now() WHERE id=$1 AND lease_owner=$2`, windowID, owner, status)
	return err
}
