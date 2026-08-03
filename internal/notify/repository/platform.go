package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/notify/model"
	notifytemplate "github.com/herdifirdausss/seev/internal/notify/template"
	"github.com/herdifirdausss/seev/pkg/database"
)

var (
	ErrSettingsConflict   = errors.New("notify: notification settings version conflict")
	ErrPreferenceConflict = errors.New("notify: notification preference version conflict")
	ErrDeviceConflict     = errors.New("notify: device token belongs to another user")
	ErrDeliveryNotFound   = errors.New("notify: delivery not found")
)

// PlatformRepository is the C3 persistence boundary. The original
// Repository interface remains unchanged for old callers and tests; the
// concrete Gateway repository implements both interfaces during the additive
// migration period.
type PlatformRepository interface {
	Plan(ctx context.Context, plan model.PlannedEvent) (bool, error)
	RecordEventFailure(ctx context.Context, inbox model.EventInbox, errorCode string) error
	GetNotification(ctx context.Context, userID, id uuid.UUID) (model.Notification, error)
	ListNotifications(ctx context.Context, userID uuid.UUID, limit int, before time.Time, unread bool, category, kind string) ([]model.Notification, error)
	UnreadCount(ctx context.Context, userID uuid.UUID) (int64, error)
	MarkRead(ctx context.Context, id, userID uuid.UUID) (bool, error)
	MarkAllRead(ctx context.Context, userID uuid.UUID, before *time.Time) error
	GetSettings(ctx context.Context, userID uuid.UUID) (model.UserSettings, error)
	PutSettings(ctx context.Context, settings model.UserSettings, expectedVersion int64) (model.UserSettings, error)
	ListPreferences(ctx context.Context, userID uuid.UUID) ([]model.Preference, error)
	ReplacePreferences(ctx context.Context, userID uuid.UUID, preferences []model.Preference) ([]model.Preference, error)
	RegisterDevice(ctx context.Context, endpoint model.DeviceEndpoint) (model.DeviceEndpoint, error)
	FindDeviceByFingerprint(ctx context.Context, userID uuid.UUID, fingerprint []byte) (model.DeviceEndpoint, bool, error)
	ListDevices(ctx context.Context, userID uuid.UUID) ([]model.DeviceEndpoint, error)
	ListActiveDevices(ctx context.Context, userID uuid.UUID) ([]model.DeviceEndpoint, error)
	RevokeDevice(ctx context.Context, userID, deviceID uuid.UUID) error
	GetDevice(ctx context.Context, userID, deviceID uuid.UUID) (model.DeviceEndpoint, error)
	DeliveryHealth(ctx context.Context, channel string) (map[string]int64, *time.Time, error)
	UpdateDeliverySnapshot(ctx context.Context, deliveryID uuid.UUID, version notifytemplate.Version, rendered notifytemplate.Rendered) error
	SetRecipientSnapshot(ctx context.Context, deliveryID uuid.UUID, owner string, ciphertext []byte, keyVersion int, fingerprint []byte) error
	ClaimDue(ctx context.Context, channel string, limit int, owner string, leaseUntil time.Time) ([]model.Delivery, error)
	FinishDelivery(ctx context.Context, deliveryID uuid.UUID, owner, providerMessageID string) error
	RetryDelivery(ctx context.Context, deliveryID uuid.UUID, owner, code string, next time.Time) error
	SuppressDelivery(ctx context.Context, deliveryID uuid.UUID, owner, code string) error
	DeadDelivery(ctx context.Context, deliveryID uuid.UUID, owner, code string) error
	InsertAttempt(ctx context.Context, attempt model.DeliveryAttempt) error
	MarkDeviceInvalid(ctx context.Context, endpointID uuid.UUID, code string) error
	ReplayDelivery(ctx context.Context, deliveryID uuid.UUID, next time.Time) error
	ListDeliveries(ctx context.Context, status, channel string, limit int) ([]model.Delivery, error)
	GetDelivery(ctx context.Context, deliveryID uuid.UUID) (model.Delivery, error)
	GetChannelControl(ctx context.Context, channel string) (model.ChannelControl, error)
	SetChannelControl(ctx context.Context, control model.ChannelControl) error
	GetActiveTemplate(ctx context.Context, kind, channel, locale string) (notifytemplate.Version, bool, error)
	GetTemplateVersion(ctx context.Context, id uuid.UUID) (notifytemplate.Version, bool, error)
	ListTemplateVersions(ctx context.Context, kind, channel, locale string) ([]notifytemplate.Version, error)
	CreateTemplateDraft(ctx context.Context, version notifytemplate.Version, actor string) error
	SubmitTemplate(ctx context.Context, id uuid.UUID, actor string) error
	ApproveTemplate(ctx context.Context, id uuid.UUID, actor string) error
	RejectTemplate(ctx context.Context, id uuid.UUID, actor, reason string) error
	RetireTemplate(ctx context.Context, id uuid.UUID, actor string) error
	AddDigestItem(ctx context.Context, request model.DigestRequest) error
	ClaimDigestWindows(ctx context.Context, limit int, owner string, leaseUntil time.Time) ([]model.DigestWindow, error)
	ListDigestNotifications(ctx context.Context, windowID, userID uuid.UUID) ([]model.Notification, error)
	CreateDigestDelivery(ctx context.Context, window model.DigestWindow, subject, text, html string, templateID uuid.UUID, hash []byte) error
	FinishDigestWindow(ctx context.Context, windowID uuid.UUID, owner, status string) error
}

func (r *platformRepo) RecordEventFailure(ctx context.Context, inbox model.EventInbox, errorCode string) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO notif_event_inbox
			(id,source_service,event_id,event_type,schema_version,payload_hash,status,error_code,received_at,created_at,updated_at)
		VALUES($1,$2,$3,$4,$5,$6,'failed',$7,COALESCE($8,now()),now(),now())
		ON CONFLICT(source_service,event_id) DO UPDATE SET status='failed',error_code=EXCLUDED.error_code,updated_at=now()`,
		inbox.ID, inbox.SourceService, inbox.EventID, inbox.EventType, inbox.SchemaVersion, inbox.PayloadHash,
		nullableString(errorCode), nullableTime(inbox.ReceivedAt))
	return err
}

type platformRepo struct{ db database.DatabaseSQL }

func NewPlatformRepository(db database.DatabaseSQL) PlatformRepository { return &platformRepo{db: db} }

func (r *platformRepo) Plan(ctx context.Context, plan model.PlannedEvent) (inserted bool, err error) {
	err = r.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
			INSERT INTO notif_event_inbox
				(id, source_service, event_id, event_type, schema_version, payload_hash, status, received_at, created_at, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,'received',COALESCE($7,now()),now(),now())
			ON CONFLICT (source_service,event_id) DO NOTHING`,
			plan.Inbox.ID, plan.Inbox.SourceService, plan.Inbox.EventID, plan.Inbox.EventType,
			plan.Inbox.SchemaVersion, plan.Inbox.PayloadHash, nullableTime(plan.Inbox.ReceivedAt))
		if err != nil {
			return fmt.Errorf("insert notification event inbox: %w", err)
		}
		count, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("notification event inbox rows affected: %w", err)
		}
		// The inbox is unique per source event, while one event may fan out to
		// multiple recipients. An already-seen inbox row therefore must not
		// short-circuit this transaction: the notification's own
		// (event,user,kind) uniqueness is the logical dedup guard and lets the
		// second recipient of a transfer be planned in its own call.
		_ = count
		inserted = true

		n := plan.Notification
		var notificationID uuid.UUID
		err = tx.QueryRowContext(ctx, `
			INSERT INTO notif_notifications
				(id,user_id,event_id,type,title,body,payload,event_type,source_service,kind,category,priority,requirement,locale,template_version_id,deep_link,context,content_hash,expires_at,created_at,updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,COALESCE($20,now()),COALESCE($20,now()))
			ON CONFLICT DO NOTHING
			RETURNING id`,
			n.ID, n.UserID, n.EventID, n.Type, n.Title, n.Body, jsonOrEmpty(n.Payload),
			n.EventType, n.SourceService, n.Kind, n.Category, n.Priority, n.Requirement, n.Locale,
			nullableUUID(n.TemplateVersionID), nullableString(n.DeepLink), jsonOrEmpty(n.Context), n.ContentHash,
			n.ExpiresAt, nullableTime(n.CreatedAt)).Scan(&notificationID)
		if errors.Is(err, sql.ErrNoRows) {
			inserted = false
			// During the expand/contract window an old binary may have already
			// inserted the same event/user pair under the legacy narrower unique
			// constraint. Reuse that logical row rather than creating orphaned
			// delivery plans. A legacy row is upgraded in place only after the
			// conflict has been resolved; its old raw payload remains governed by
			// the existing retention policy.
			err = tx.QueryRowContext(ctx, `
				SELECT id FROM notif_notifications
				WHERE event_id=$1 AND user_id=$2 AND (kind=$3 OR kind='legacy')
				ORDER BY CASE WHEN kind=$3 THEN 0 ELSE 1 END, created_at, id
				LIMIT 1`, n.EventID, n.UserID, n.Kind).Scan(&notificationID)
			if err == nil {
				if _, updateErr := tx.ExecContext(ctx, `
					UPDATE notif_notifications SET event_type=$2,source_service=$3,kind=$4,category=$5,
					priority=$6,requirement=$7,locale=$8,template_version_id=$9,deep_link=$10,
					context=$11,content_hash=$12,updated_at=now()
					WHERE id=$1 AND kind='legacy'`, notificationID, n.EventType, n.SourceService, n.Kind,
					n.Category, n.Priority, n.Requirement, n.Locale, nullableUUID(n.TemplateVersionID),
					n.DeepLink, jsonOrEmpty(n.Context), n.ContentHash); updateErr != nil {
					return fmt.Errorf("upgrade legacy notification: %w", updateErr)
				}
			}
		}
		if err != nil {
			return fmt.Errorf("insert planned notification: %w", err)
		}
		n.ID = notificationID
		for _, delivery := range plan.Deliveries {
			delivery.NotificationID = &notificationID
			if err := insertDelivery(ctx, tx, delivery); err != nil {
				return err
			}
		}
		for _, digestItem := range plan.DigestItems {
			digestItem.NotificationID = notificationID
			if err := insertDigestItemTx(ctx, tx, digestItem); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE notif_event_inbox SET status='processed', processed_at=now(), updated_at=now()
			WHERE source_service=$1 AND event_id=$2`, plan.Inbox.SourceService, plan.Inbox.EventID); err != nil {
			return fmt.Errorf("mark notification event processed: %w", err)
		}
		return nil
	})
	return inserted, err
}

func insertDelivery(ctx context.Context, tx *sql.Tx, d model.Delivery) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO notif_deliveries
			(id,notification_id,digest_window_id,user_id,channel,endpoint_id,endpoint_identity,status,template_version_id,locale,
			recipient_ciphertext,recipient_key_version,recipient_fingerprint,rendered_subject,rendered_title,rendered_text,rendered_html,
			provider_payload,content_hash,attempt_count,next_attempt_at,created_at,updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,COALESCE($23,now()),COALESCE($23,now()))
		ON CONFLICT (notification_id,channel,endpoint_identity) DO NOTHING`,
		d.ID, nullableUUID(d.NotificationID), nullableUUID(d.DigestWindowID), d.UserID, d.Channel,
		nullableUUID(d.EndpointID), d.EndpointIdentity, d.Status, d.TemplateVersionID, d.Locale,
		nullableBytes(d.RecipientCiphertext), nullableInt(d.RecipientKeyVersion), nullableBytes(d.RecipientFingerprint),
		nullableString(d.RenderedSubject), nullableString(d.RenderedTitle), d.RenderedText, nullableString(d.RenderedHTML),
		nullableJSON(d.ProviderPayload), d.ContentHash, d.AttemptCount, nullableTimePtr(d.NextAttemptAt), nullableTime(d.CreatedAt))
	if err != nil {
		return fmt.Errorf("insert notification delivery: %w", err)
	}
	return nil
}

func (r *platformRepo) GetNotification(ctx context.Context, userID, id uuid.UUID) (model.Notification, error) {
	row := r.db.QueryRowContext(ctx, notificationSelect+` WHERE user_id=$1 AND id=$2`, userID, id)
	n, err := scanModernNotification(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Notification{}, ErrNotFound
	}
	return n, err
}

func (r *platformRepo) ListNotifications(ctx context.Context, userID uuid.UUID, limit int, before time.Time, unread bool, category, kind string) ([]model.Notification, error) {
	query := notificationSelect + ` WHERE user_id=$1`
	args := []any{userID}
	if !before.IsZero() {
		query += ` AND created_at < $` + fmt.Sprint(len(args)+1)
		args = append(args, before)
	}
	if unread {
		query += ` AND read_at IS NULL`
	}
	if category != "" {
		query += ` AND category = $` + fmt.Sprint(len(args)+1)
		args = append(args, category)
	}
	if kind != "" {
		query += ` AND kind = $` + fmt.Sprint(len(args)+1)
		args = append(args, kind)
	}
	query += ` ORDER BY created_at DESC, id DESC LIMIT $` + fmt.Sprint(len(args)+1)
	args = append(args, limit)
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list modern notifications: %w", err)
	}
	defer rows.Close()
	var out []model.Notification
	for rows.Next() {
		n, err := scanModernNotification(rows)
		if err != nil {
			return nil, fmt.Errorf("scan modern notification: %w", err)
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (r *platformRepo) UnreadCount(ctx context.Context, userID uuid.UUID) (int64, error) {
	var count int64
	err := r.db.QueryRowContext(ctx, `SELECT count(*) FROM notif_notifications WHERE user_id=$1 AND read_at IS NULL`, userID).Scan(&count)
	return count, err
}

func (r *platformRepo) MarkRead(ctx context.Context, id, userID uuid.UUID) (bool, error) {
	result, err := r.db.ExecContext(ctx, `UPDATE notif_notifications SET read_at=COALESCE(read_at,now()),updated_at=now() WHERE id=$1 AND user_id=$2`, id, userID)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if count > 0 {
		return true, nil
	}
	var exists bool
	if err := r.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM notif_notifications WHERE id=$1 AND user_id=$2)`, id, userID).Scan(&exists); err != nil {
		return false, err
	}
	return exists, nil
}

func (r *platformRepo) MarkAllRead(ctx context.Context, userID uuid.UUID, before *time.Time) error {
	if before == nil {
		_, err := r.db.ExecContext(ctx, `UPDATE notif_notifications SET read_at=COALESCE(read_at,now()), updated_at=now() WHERE user_id=$1 AND read_at IS NULL`, userID)
		return err
	}
	_, err := r.db.ExecContext(ctx, `UPDATE notif_notifications SET read_at=COALESCE(read_at,now()), updated_at=now() WHERE user_id=$1 AND read_at IS NULL AND created_at <= $2`, userID, *before)
	return err
}

func (r *platformRepo) GetSettings(ctx context.Context, userID uuid.UUID) (model.UserSettings, error) {
	var s model.UserSettings
	var start, end string
	err := r.db.QueryRowContext(ctx, `
		SELECT user_id,locale,timezone,quiet_hours_enabled,COALESCE(to_char(quiet_hours_start,'HH24:MI'),''),COALESCE(to_char(quiet_hours_end,'HH24:MI'),''),to_char(daily_digest_hour,'HH24:MI'),version,created_at,updated_at
		FROM notif_user_settings WHERE user_id=$1`, userID).Scan(&s.UserID, &s.Locale, &s.Timezone, &s.QuietHoursEnabled, &start, &end, &s.DailyDigestHour, &s.Version, &s.CreatedAt, &s.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return model.UserSettings{UserID: userID, Locale: "en-US", Timezone: "Asia/Jakarta", DailyDigestHour: "08:00", Version: 1}, nil
	}
	if err != nil {
		return s, fmt.Errorf("get notification settings: %w", err)
	}
	if start != "" {
		s.QuietHoursStart = &start
	}
	if end != "" {
		s.QuietHoursEnd = &end
	}
	return s, nil
}

func (r *platformRepo) PutSettings(ctx context.Context, s model.UserSettings, expected int64) (model.UserSettings, error) {
	var out model.UserSettings
	var start, end any
	if s.QuietHoursStart != nil {
		start = *s.QuietHoursStart
	}
	if s.QuietHoursEnd != nil {
		end = *s.QuietHoursEnd
	}
	var startOut, endOut string
	err := r.db.QueryRowContext(ctx, `
		INSERT INTO notif_user_settings(user_id,locale,timezone,quiet_hours_enabled,quiet_hours_start,quiet_hours_end,daily_digest_hour,version)
		VALUES($1,$2,$3,$4,$5,$6,$7,1)
		ON CONFLICT(user_id) DO UPDATE SET locale=EXCLUDED.locale,timezone=EXCLUDED.timezone,quiet_hours_enabled=EXCLUDED.quiet_hours_enabled,
		quiet_hours_start=EXCLUDED.quiet_hours_start,quiet_hours_end=EXCLUDED.quiet_hours_end,daily_digest_hour=EXCLUDED.daily_digest_hour,
		version=notif_user_settings.version+1,updated_at=now() WHERE notif_user_settings.version=$8
		RETURNING user_id,locale,timezone,quiet_hours_enabled,COALESCE(to_char(quiet_hours_start,'HH24:MI'),''),COALESCE(to_char(quiet_hours_end,'HH24:MI'),''),to_char(daily_digest_hour,'HH24:MI'),version,created_at,updated_at`,
		s.UserID, s.Locale, s.Timezone, s.QuietHoursEnabled, start, end, s.DailyDigestHour, expected).Scan(&out.UserID, &out.Locale, &out.Timezone, &out.QuietHoursEnabled, &startOut, &endOut, &out.DailyDigestHour, &out.Version, &out.CreatedAt, &out.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return model.UserSettings{}, ErrSettingsConflict
	}
	if err != nil {
		return model.UserSettings{}, fmt.Errorf("put notification settings: %w", err)
	}
	if startOut != "" {
		out.QuietHoursStart = &startOut
	}
	if endOut != "" {
		out.QuietHoursEnd = &endOut
	}
	return out, nil
}

func (r *platformRepo) ListPreferences(ctx context.Context, userID uuid.UUID) ([]model.Preference, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id,user_id,category,channel,mode,version FROM notif_preferences WHERE user_id=$1 ORDER BY category,channel`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Preference
	for rows.Next() {
		var p model.Preference
		if err := rows.Scan(&p.ID, &p.UserID, &p.Category, &p.Channel, &p.Mode, &p.Version); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *platformRepo) ReplacePreferences(ctx context.Context, userID uuid.UUID, preferences []model.Preference) ([]model.Preference, error) {
	err := r.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		for _, p := range preferences {
			var current int64
			err := tx.QueryRowContext(ctx, `SELECT version FROM notif_preferences WHERE user_id=$1 AND category=$2 AND channel=$3`, userID, p.Category, p.Channel).Scan(&current)
			if errors.Is(err, sql.ErrNoRows) {
				if p.Version > 1 {
					return ErrPreferenceConflict
				}
				if _, err := tx.ExecContext(ctx, `INSERT INTO notif_preferences(id,user_id,category,channel,mode,version) VALUES($1,$2,$3,$4,$5,1) ON CONFLICT(user_id,category,channel) DO NOTHING`, uuidOrNew(p.ID), userID, p.Category, p.Channel, p.Mode); err != nil {
					return err
				}
				continue
			}
			if err != nil {
				return err
			}
			if p.Version > 0 && p.Version != current {
				return ErrPreferenceConflict
			}
			if _, err := tx.ExecContext(ctx, `UPDATE notif_preferences SET mode=$4,version=version+1,updated_at=now() WHERE user_id=$1 AND category=$2 AND channel=$3`, userID, p.Category, p.Channel, p.Mode); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("replace notification preferences: %w", err)
	}
	return r.ListPreferences(ctx, userID)
}

func (r *platformRepo) RegisterDevice(ctx context.Context, d model.DeviceEndpoint) (model.DeviceEndpoint, error) {
	var other uuid.UUID
	err := r.db.QueryRowContext(ctx, `SELECT user_id FROM notif_device_endpoints WHERE token_fingerprint=$1 LIMIT 1`, d.TokenFingerprint).Scan(&other)
	if err == nil && other != d.UserID {
		return model.DeviceEndpoint{}, ErrDeviceConflict
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return model.DeviceEndpoint{}, err
	}
	var out model.DeviceEndpoint
	err = r.db.QueryRowContext(ctx, `
		INSERT INTO notif_device_endpoints(id,user_id,platform,device_name,token_ciphertext,token_key_version,token_fingerprint,token_suffix,status)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,'active')
		ON CONFLICT(user_id,token_fingerprint) DO UPDATE SET platform=EXCLUDED.platform,device_name=EXCLUDED.device_name,
			 token_ciphertext=EXCLUDED.token_ciphertext,token_key_version=EXCLUDED.token_key_version,
			 token_suffix=EXCLUDED.token_suffix,status='active',revoked_at=NULL,updated_at=now()
		RETURNING id,user_id,platform,COALESCE(device_name,''),token_ciphertext,token_key_version,token_fingerprint,COALESCE(token_suffix,''),status,last_success_at,last_failure_at,COALESCE(last_failure_code,''),created_at,updated_at,revoked_at`,
		d.ID, d.UserID, d.Platform, d.DeviceName, d.TokenCiphertext, d.TokenKeyVersion, d.TokenFingerprint, d.TokenSuffix).Scan(
		&out.ID, &out.UserID, &out.Platform, &out.DeviceName, &out.TokenCiphertext, &out.TokenKeyVersion, &out.TokenFingerprint, &out.TokenSuffix, &out.Status, &out.LastSuccessAt, &out.LastFailureAt, &out.LastFailureCode, &out.CreatedAt, &out.UpdatedAt, &out.RevokedAt)
	if err != nil {
		return model.DeviceEndpoint{}, fmt.Errorf("register notification device: %w", err)
	}
	return out, nil
}

func (r *platformRepo) FindDeviceByFingerprint(ctx context.Context, userID uuid.UUID, fingerprint []byte) (model.DeviceEndpoint, bool, error) {
	var d model.DeviceEndpoint
	err := r.db.QueryRowContext(ctx, `
		SELECT id,user_id,platform,COALESCE(device_name,''),token_ciphertext,token_key_version,
			token_fingerprint,COALESCE(token_suffix,''),status,last_success_at,last_failure_at,
			COALESCE(last_failure_code,''),created_at,updated_at,revoked_at
		FROM notif_device_endpoints WHERE user_id=$1 AND token_fingerprint=$2`, userID, fingerprint).Scan(
		&d.ID, &d.UserID, &d.Platform, &d.DeviceName, &d.TokenCiphertext, &d.TokenKeyVersion,
		&d.TokenFingerprint, &d.TokenSuffix, &d.Status, &d.LastSuccessAt, &d.LastFailureAt,
		&d.LastFailureCode, &d.CreatedAt, &d.UpdatedAt, &d.RevokedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return model.DeviceEndpoint{}, false, nil
	}
	if err != nil {
		return model.DeviceEndpoint{}, false, err
	}
	return d, true, nil
}

func (r *platformRepo) ListDevices(ctx context.Context, userID uuid.UUID) ([]model.DeviceEndpoint, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id,user_id,platform,COALESCE(device_name,''),token_ciphertext,token_key_version,token_fingerprint,COALESCE(token_suffix,''),status,last_success_at,last_failure_at,COALESCE(last_failure_code,''),created_at,updated_at,revoked_at FROM notif_device_endpoints WHERE user_id=$1 ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.DeviceEndpoint
	for rows.Next() {
		var d model.DeviceEndpoint
		if err := rows.Scan(&d.ID, &d.UserID, &d.Platform, &d.DeviceName, &d.TokenCiphertext, &d.TokenKeyVersion, &d.TokenFingerprint, &d.TokenSuffix, &d.Status, &d.LastSuccessAt, &d.LastFailureAt, &d.LastFailureCode, &d.CreatedAt, &d.UpdatedAt, &d.RevokedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (r *platformRepo) ListActiveDevices(ctx context.Context, userID uuid.UUID) ([]model.DeviceEndpoint, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id,user_id,platform,COALESCE(device_name,''),token_ciphertext,token_key_version,token_fingerprint,COALESCE(token_suffix,''),status,last_success_at,last_failure_at,COALESCE(last_failure_code,''),created_at,updated_at,revoked_at FROM notif_device_endpoints WHERE user_id=$1 AND status='active' ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.DeviceEndpoint
	for rows.Next() {
		var d model.DeviceEndpoint
		if err := rows.Scan(&d.ID, &d.UserID, &d.Platform, &d.DeviceName, &d.TokenCiphertext, &d.TokenKeyVersion, &d.TokenFingerprint, &d.TokenSuffix, &d.Status, &d.LastSuccessAt, &d.LastFailureAt, &d.LastFailureCode, &d.CreatedAt, &d.UpdatedAt, &d.RevokedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (r *platformRepo) RevokeDevice(ctx context.Context, userID, deviceID uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `UPDATE notif_device_endpoints SET status='revoked',revoked_at=COALESCE(revoked_at,now()),updated_at=now() WHERE id=$1 AND user_id=$2`, deviceID, userID)
	return err
}

func (r *platformRepo) GetDevice(ctx context.Context, userID, deviceID uuid.UUID) (model.DeviceEndpoint, error) {
	var d model.DeviceEndpoint
	err := r.db.QueryRowContext(ctx, `SELECT id,user_id,platform,COALESCE(device_name,''),token_ciphertext,token_key_version,token_fingerprint,COALESCE(token_suffix,''),status,last_success_at,last_failure_at,COALESCE(last_failure_code,''),created_at,updated_at,revoked_at FROM notif_device_endpoints WHERE id=$1 AND user_id=$2`, deviceID, userID).Scan(&d.ID, &d.UserID, &d.Platform, &d.DeviceName, &d.TokenCiphertext, &d.TokenKeyVersion, &d.TokenFingerprint, &d.TokenSuffix, &d.Status, &d.LastSuccessAt, &d.LastFailureAt, &d.LastFailureCode, &d.CreatedAt, &d.UpdatedAt, &d.RevokedAt)
	return d, err
}

func (r *platformRepo) DeliveryHealth(ctx context.Context, channel string) (map[string]int64, *time.Time, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT status, count(*), min(
			CASE
				WHEN status IN ('pending_recipient','scheduled','retry_wait')
					 AND (next_attempt_at IS NULL OR next_attempt_at <= now())
					THEN COALESCE(next_attempt_at, created_at)
				WHEN status = 'processing'
					 AND lease_expires_at IS NOT NULL AND lease_expires_at <= now()
					THEN COALESCE(next_attempt_at, created_at)
			END
		) AS oldest_due
		FROM notif_deliveries
		WHERE channel=$1
		  AND status IN ('pending_recipient','scheduled','retry_wait','processing','dead','blocked')
		GROUP BY status`, channel)
	if err != nil {
		return nil, nil, fmt.Errorf("notification delivery health: %w", err)
	}
	defer rows.Close()
	counts := make(map[string]int64)
	var oldest sql.NullTime
	for rows.Next() {
		var status string
		var count int64
		var candidate sql.NullTime
		if err := rows.Scan(&status, &count, &candidate); err != nil {
			return nil, nil, fmt.Errorf("scan notification delivery health: %w", err)
		}
		counts[status] = count
		if candidate.Valid && (!oldest.Valid || candidate.Time.Before(oldest.Time)) {
			oldest = candidate
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	if oldest.Valid {
		value := oldest.Time
		return counts, &value, nil
	}
	return counts, nil, nil
}

func (r *platformRepo) SetRecipientSnapshot(ctx context.Context, id uuid.UUID, owner string, ciphertext []byte, keyVersion int, fingerprint []byte) error {
	_, err := r.db.ExecContext(ctx, `UPDATE notif_deliveries SET recipient_ciphertext=$3,recipient_key_version=$4,recipient_fingerprint=$5,status='scheduled',next_attempt_at=now(),updated_at=now() WHERE id=$1 AND lease_owner=$2`, id, owner, ciphertext, keyVersion, fingerprint)
	return err
}

func (r *platformRepo) UpdateDeliverySnapshot(ctx context.Context, id uuid.UUID, version notifytemplate.Version, rendered notifytemplate.Rendered) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE notif_deliveries
		SET template_version_id=$2, locale=$3, rendered_subject=$4, rendered_title=$5,
			rendered_text=$6, rendered_html=$7, provider_payload=$8, content_hash=$9, updated_at=now()
		WHERE id=$1 AND status='blocked'`,
		id, version.ID, version.Locale, nullableString(rendered.Subject), nullableString(rendered.Title),
		rendered.Text, nullableString(rendered.HTML), nullableJSON(rendered.Payload), rendered.Hash)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrDeliveryNotFound
	}
	return nil
}

func (r *platformRepo) ClaimDue(ctx context.Context, channel string, limit int, owner string, leaseUntil time.Time) ([]model.Delivery, error) {
	rows, err := r.db.QueryContext(ctx, `WITH claimed AS (SELECT id FROM notif_deliveries WHERE channel=$1 AND status IN ('pending_recipient','scheduled','retry_wait') AND (next_attempt_at IS NULL OR next_attempt_at<=now()) AND (lease_expires_at IS NULL OR lease_expires_at<now()) ORDER BY COALESCE(next_attempt_at,created_at),id LIMIT $2 FOR UPDATE SKIP LOCKED) UPDATE notif_deliveries d SET status='processing',lease_owner=$3,lease_expires_at=$4,attempt_count=d.attempt_count+1,updated_at=now() FROM claimed WHERE d.id=claimed.id RETURNING d.id,d.notification_id,d.digest_window_id,d.user_id,d.channel,d.endpoint_id,d.endpoint_identity,d.status,d.template_version_id,d.locale,d.recipient_ciphertext,d.recipient_key_version,d.recipient_fingerprint,COALESCE(d.rendered_subject,''),COALESCE(d.rendered_title,''),d.rendered_text,COALESCE(d.rendered_html,''),COALESCE(d.provider_payload,'{}'::jsonb),d.content_hash,d.attempt_count,d.next_attempt_at,COALESCE(d.lease_owner,''),d.lease_expires_at,COALESCE(d.provider_message_id,''),COALESCE(d.last_error_code,''),d.delivered_at,d.suppressed_at,d.dead_at,d.created_at,d.updated_at`, channel, limit, owner, leaseUntil)
	if err != nil {
		return nil, fmt.Errorf("claim notification deliveries: %w", err)
	}
	defer rows.Close()
	var out []model.Delivery
	for rows.Next() {
		d, err := scanDelivery(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (r *platformRepo) FinishDelivery(ctx context.Context, id uuid.UUID, owner, messageID string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE notif_deliveries SET status='delivered',provider_message_id=NULLIF($3,''),delivered_at=now(),lease_owner=NULL,lease_expires_at=NULL,updated_at=now() WHERE id=$1 AND lease_owner=$2`, id, owner, messageID)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `UPDATE notif_digest_windows SET status='delivered',lease_owner=NULL,lease_expires_at=NULL,updated_at=now() WHERE delivery_id=$1`, id)
	return err
}
func (r *platformRepo) RetryDelivery(ctx context.Context, id uuid.UUID, owner, code string, next time.Time) error {
	_, err := r.db.ExecContext(ctx, `UPDATE notif_deliveries SET status='retry_wait',last_error_code=$3,next_attempt_at=$4,lease_owner=NULL,lease_expires_at=NULL,updated_at=now() WHERE id=$1 AND lease_owner=$2`, id, owner, code, next)
	return err
}
func (r *platformRepo) SuppressDelivery(ctx context.Context, id uuid.UUID, owner, code string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE notif_deliveries SET status='suppressed',last_error_code=$3,suppressed_at=now(),lease_owner=NULL,lease_expires_at=NULL,updated_at=now() WHERE id=$1 AND lease_owner=$2`, id, owner, code)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `UPDATE notif_digest_windows SET status='suppressed',lease_owner=NULL,lease_expires_at=NULL,updated_at=now() WHERE delivery_id=$1`, id)
	return err
}
func (r *platformRepo) DeadDelivery(ctx context.Context, id uuid.UUID, owner, code string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE notif_deliveries SET status='dead',last_error_code=$3,dead_at=now(),lease_owner=NULL,lease_expires_at=NULL,updated_at=now() WHERE id=$1 AND lease_owner=$2`, id, owner, code)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `UPDATE notif_digest_windows SET status='dead',lease_owner=NULL,lease_expires_at=NULL,updated_at=now() WHERE delivery_id=$1`, id)
	return err
}
func (r *platformRepo) InsertAttempt(ctx context.Context, a model.DeliveryAttempt) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO notif_delivery_attempts(id,delivery_id,attempt_number,lease_owner,provider,started_at,finished_at,result,status_class,provider_message_id,error_code,duration_ms,response_excerpt) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14) ON CONFLICT(delivery_id,attempt_number) DO NOTHING`, a.ID, a.DeliveryID, a.AttemptNumber, a.LeaseOwner, a.Provider, a.StartedAt, nullableTimePtr(a.FinishedAt), a.Result, nullableString(a.StatusClass), nullableString(a.ProviderMessageID), nullableString(a.ErrorCode), nullableIntValue(a.DurationMS), nullableString(a.ResponseExcerpt))
	return err
}
func (r *platformRepo) MarkDeviceInvalid(ctx context.Context, id uuid.UUID, code string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE notif_device_endpoints SET status='invalid',last_failure_at=now(),last_failure_code=$2,updated_at=now() WHERE id=$1`, id, code)
	return err
}
func (r *platformRepo) ReplayDelivery(ctx context.Context, id uuid.UUID, next time.Time) error {
	_, err := r.db.ExecContext(ctx, `UPDATE notif_deliveries SET status='scheduled',attempt_count=0,next_attempt_at=$2,last_error_code=NULL,dead_at=NULL,suppressed_at=NULL,lease_owner=NULL,lease_expires_at=NULL,updated_at=now() WHERE id=$1 AND status IN ('dead','blocked')`, id, next)
	return err
}

func (r *platformRepo) ListDeliveries(ctx context.Context, status, channel string, limit int) ([]model.Delivery, error) {
	query := `SELECT id,notification_id,digest_window_id,user_id,channel,endpoint_id,endpoint_identity,status,template_version_id,locale,recipient_ciphertext,recipient_key_version,recipient_fingerprint,COALESCE(rendered_subject,''),COALESCE(rendered_title,''),rendered_text,COALESCE(rendered_html,''),COALESCE(provider_payload,'{}'::jsonb),content_hash,attempt_count,next_attempt_at,COALESCE(lease_owner,''),lease_expires_at,COALESCE(provider_message_id,''),COALESCE(last_error_code,''),delivered_at,suppressed_at,dead_at,created_at,updated_at FROM notif_deliveries WHERE ($1='' OR status=$1) AND ($2='' OR channel=$2) ORDER BY created_at DESC LIMIT $3`
	rows, err := r.db.QueryContext(ctx, query, status, channel, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Delivery
	for rows.Next() {
		d, err := scanDelivery(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
func (r *platformRepo) GetDelivery(ctx context.Context, id uuid.UUID) (model.Delivery, error) {
	d, err := scanDelivery(r.db.QueryRowContext(ctx, `SELECT id,notification_id,digest_window_id,user_id,channel,endpoint_id,endpoint_identity,status,template_version_id,locale,recipient_ciphertext,recipient_key_version,recipient_fingerprint,COALESCE(rendered_subject,''),COALESCE(rendered_title,''),rendered_text,COALESCE(rendered_html,''),COALESCE(provider_payload,'{}'::jsonb),content_hash,attempt_count,next_attempt_at,COALESCE(lease_owner,''),lease_expires_at,COALESCE(provider_message_id,''),COALESCE(last_error_code,''),delivered_at,suppressed_at,dead_at,created_at,updated_at FROM notif_deliveries WHERE id=$1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return model.Delivery{}, ErrDeliveryNotFound
	}
	return d, err
}
func (r *platformRepo) GetChannelControl(ctx context.Context, channel string) (model.ChannelControl, error) {
	var c model.ChannelControl
	err := r.db.QueryRowContext(ctx, `SELECT channel,state,COALESCE(reason,''),changed_by,changed_at,expires_at,version FROM notif_channel_controls WHERE channel=$1`, channel).Scan(&c.Channel, &c.State, &c.Reason, &c.ChangedBy, &c.ChangedAt, &c.ExpiresAt, &c.Version)
	return c, err
}
func (r *platformRepo) SetChannelControl(ctx context.Context, c model.ChannelControl) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO notif_channel_controls(channel,state,reason,changed_by,expires_at,version) VALUES($1,$2,$3,$4,$5,1) ON CONFLICT(channel) DO UPDATE SET state=EXCLUDED.state,reason=EXCLUDED.reason,changed_by=EXCLUDED.changed_by,changed_at=now(),expires_at=EXCLUDED.expires_at,version=notif_channel_controls.version+1`, c.Channel, c.State, c.Reason, c.ChangedBy, c.ExpiresAt)
	return err
}

const notificationSelect = `SELECT id,user_id,event_id,type,title,body,payload,read_at,created_at,event_type,source_service,kind,category,priority,requirement,locale,template_version_id,COALESCE(deep_link,''),context,content_hash,expires_at,updated_at FROM notif_notifications`

type scanner interface{ Scan(...any) error }

func scanModernNotification(s scanner) (model.Notification, error) {
	var n model.Notification
	var readAt sql.NullTime
	var templateID uuid.NullUUID
	var payload, context, hash []byte
	if err := s.Scan(&n.ID, &n.UserID, &n.EventID, &n.Type, &n.Title, &n.Body, &payload, &readAt, &n.CreatedAt, &n.EventType, &n.SourceService, &n.Kind, &n.Category, &n.Priority, &n.Requirement, &n.Locale, &templateID, &n.DeepLink, &context, &hash, &n.ExpiresAt, &n.UpdatedAt); err != nil {
		return n, err
	}
	n.Payload = payload
	n.Context = context
	n.ContentHash = hash
	if readAt.Valid {
		n.ReadAt = &readAt.Time
	}
	if templateID.Valid {
		n.TemplateVersionID = &templateID.UUID
	}
	return n, nil
}
func scanDelivery(s scanner) (model.Delivery, error) {
	var d model.Delivery
	var providerPayload []byte
	var notificationID, digestID, endpointID uuid.NullUUID
	var keyVersion sql.NullInt64
	if err := s.Scan(&d.ID, &notificationID, &digestID, &d.UserID, &d.Channel, &endpointID, &d.EndpointIdentity, &d.Status, &d.TemplateVersionID, &d.Locale, &d.RecipientCiphertext, &keyVersion, &d.RecipientFingerprint, &d.RenderedSubject, &d.RenderedTitle, &d.RenderedText, &d.RenderedHTML, &providerPayload, &d.ContentHash, &d.AttemptCount, &d.NextAttemptAt, &d.LeaseOwner, &d.LeaseExpiresAt, &d.ProviderMessageID, &d.LastErrorCode, &d.DeliveredAt, &d.SuppressedAt, &d.DeadAt, &d.CreatedAt, &d.UpdatedAt); err != nil {
		return d, err
	}
	if notificationID.Valid {
		d.NotificationID = &notificationID.UUID
	}
	if digestID.Valid {
		d.DigestWindowID = &digestID.UUID
	}
	if endpointID.Valid {
		d.EndpointID = &endpointID.UUID
	}
	if keyVersion.Valid {
		v := int(keyVersion.Int64)
		d.RecipientKeyVersion = &v
	}
	d.ProviderPayload = providerPayload
	return d, nil
}

func uuidOrNew(id uuid.UUID) uuid.UUID {
	if id == uuid.Nil {
		return uuid.New()
	}
	return id
}
func nullableUUID(id *uuid.UUID) any {
	if id == nil || *id == uuid.Nil {
		return nil
	}
	return *id
}
func nullableString(v string) any {
	if v == "" {
		return nil
	}
	return v
}
func nullableBytes(v []byte) any {
	if len(v) == 0 {
		return nil
	}
	return v
}
func nullableJSON(v []byte) any {
	if len(v) == 0 {
		return nil
	}
	return v
}
func nullableInt(v *int) any {
	if v == nil {
		return nil
	}
	return *v
}
func nullableIntValue(v int) any {
	if v == 0 {
		return nil
	}
	return v
}
func nullableTime(v time.Time) any {
	if v.IsZero() {
		return nil
	}
	return v
}
func nullableTimePtr(v *time.Time) any {
	if v == nil || v.IsZero() {
		return nil
	}
	return *v
}
func jsonOrEmpty(v []byte) []byte {
	if len(v) == 0 {
		return []byte(`{}`)
	}
	return v
}
