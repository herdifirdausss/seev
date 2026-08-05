package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	notifytemplate "github.com/herdifirdausss/seev/services/gateway/internal/notification/template"
)

func (r *platformRepo) GetActiveTemplate(ctx context.Context, kind, channel, locale string) (notifytemplate.Version, bool, error) {
	var v notifytemplate.Version
	err := r.db.QueryRowContext(ctx, `SELECT v.id,t.id,t.kind,v.channel,v.locale,v.version,v.status,COALESCE(v.subject_template,''),COALESCE(v.title_template,''),v.body_text_template,COALESCE(v.body_html_template,''),v.created_by,COALESCE(v.submitted_by,''),COALESCE(v.approved_by,'') FROM notif_template_versions v JOIN notif_templates t ON t.id=v.template_id WHERE t.kind=$1 AND v.channel=$2 AND v.locale=$3 AND v.status='active' ORDER BY v.version DESC LIMIT 1`, kind, channel, locale).Scan(&v.ID, &v.TemplateID, &v.Kind, &v.Channel, &v.Locale, &v.Version, &v.Status, &v.SubjectTemplate, &v.TitleTemplate, &v.BodyTextTemplate, &v.BodyHTMLTemplate, &v.CreatedBy, &v.SubmittedBy, &v.ApprovedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return notifytemplate.Version{}, false, nil
	}
	if err != nil {
		return v, false, err
	}
	return v, true, nil
}

func (r *platformRepo) GetTemplateVersion(ctx context.Context, id uuid.UUID) (notifytemplate.Version, bool, error) {
	var v notifytemplate.Version
	err := r.db.QueryRowContext(ctx, `
		SELECT v.id,t.id,t.kind,v.channel,v.locale,v.version,v.status,
			COALESCE(v.subject_template,''),COALESCE(v.title_template,''),v.body_text_template,
			COALESCE(v.body_html_template,''),v.created_by,COALESCE(v.submitted_by,''),COALESCE(v.approved_by,'')
		FROM notif_template_versions v
		JOIN notif_templates t ON t.id=v.template_id
		WHERE v.id=$1`, id).Scan(
		&v.ID, &v.TemplateID, &v.Kind, &v.Channel, &v.Locale, &v.Version, &v.Status,
		&v.SubjectTemplate, &v.TitleTemplate, &v.BodyTextTemplate, &v.BodyHTMLTemplate,
		&v.CreatedBy, &v.SubmittedBy, &v.ApprovedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return notifytemplate.Version{}, false, nil
	}
	if err != nil {
		return notifytemplate.Version{}, false, err
	}
	return v, true, nil
}

func (r *platformRepo) ListTemplateVersions(ctx context.Context, kind, channel, locale string) ([]notifytemplate.Version, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT v.id,t.id,t.kind,v.channel,v.locale,v.version,v.status,COALESCE(v.subject_template,''),COALESCE(v.title_template,''),v.body_text_template,COALESCE(v.body_html_template,''),v.created_by,COALESCE(v.submitted_by,''),COALESCE(v.approved_by,'') FROM notif_template_versions v JOIN notif_templates t ON t.id=v.template_id WHERE ($1='' OR t.kind=$1) AND ($2='' OR v.channel=$2) AND ($3='' OR v.locale=$3) ORDER BY t.kind,v.channel,v.locale,v.version DESC`, kind, channel, locale)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []notifytemplate.Version
	for rows.Next() {
		var v notifytemplate.Version
		if err := rows.Scan(&v.ID, &v.TemplateID, &v.Kind, &v.Channel, &v.Locale, &v.Version, &v.Status, &v.SubjectTemplate, &v.TitleTemplate, &v.BodyTextTemplate, &v.BodyHTMLTemplate, &v.CreatedBy, &v.SubmittedBy, &v.ApprovedBy); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (r *platformRepo) CreateTemplateDraft(ctx context.Context, v notifytemplate.Version, actor string) error {
	if v.ID == uuid.Nil {
		v.ID = uuid.New()
	}
	hash := sha256.Sum256([]byte(v.SubjectTemplate + "\x00" + v.TitleTemplate + "\x00" + v.BodyTextTemplate + "\x00" + v.BodyHTMLTemplate))
	return r.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		if v.TemplateID == uuid.Nil {
			if _, err := tx.ExecContext(ctx, `INSERT INTO notif_templates(id,kind,description,variable_schema) VALUES($1,$2,$3,$4) ON CONFLICT(kind) DO NOTHING`, uuid.New(), v.Kind, v.Kind, []byte(`{"version":1}`)); err != nil {
				return err
			}
			if err := tx.QueryRowContext(ctx, `SELECT id FROM notif_templates WHERE kind=$1`, v.Kind).Scan(&v.TemplateID); err != nil {
				return err
			}
		}
		// Version is always computed here, ignoring any caller-supplied
		// value: every seeded default kind already has an active v1, so a
		// hardcoded "default to 1" (the previous behavior) hit the unique
		// (template_id,channel,locale,version) constraint on the very first
		// draft of an existing kind — draft creation for any pre-seeded kind
		// was unconditionally broken until this query replaced it.
		var maxVersion int
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0) FROM notif_template_versions WHERE template_id=$1 AND channel=$2 AND locale=$3`, v.TemplateID, v.Channel, v.Locale).Scan(&maxVersion); err != nil {
			return err
		}
		v.Version = maxVersion + 1
		_, err := tx.ExecContext(ctx, `INSERT INTO notif_template_versions(id,template_id,channel,locale,version,status,subject_template,title_template,body_text_template,body_html_template,content_hash,created_by) VALUES($1,$2,$3,$4,$5,'draft',NULLIF($6,''),NULLIF($7,''),$8,NULLIF($9,''),$10,$11)`, v.ID, v.TemplateID, v.Channel, v.Locale, v.Version, v.SubjectTemplate, v.TitleTemplate, v.BodyTextTemplate, v.BodyHTMLTemplate, hash[:], actor)
		return err
	})
}
func (r *platformRepo) SubmitTemplate(ctx context.Context, id uuid.UUID, actor string) error {
	// Submission is a maker-only action on their own draft — unlike approve/
	// reject, there is no actor-must-differ rule here (that check belongs to
	// the checker step, further down). RowsAffected is checked explicitly
	// because a plain UPDATE with no matching row silently reports success,
	// which would leave the draft stuck invisible to checkers with the
	// caller none the wiser.
	result, err := r.db.ExecContext(ctx, `UPDATE notif_template_versions SET status='pending_approval',submitted_by=$2,submitted_at=now() WHERE id=$1 AND status='draft'`, id, actor)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return fmt.Errorf("template draft not found or already submitted")
	}
	return nil
}
func (r *platformRepo) ApproveTemplate(ctx context.Context, id uuid.UUID, actor string) error {
	return r.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		var templateID uuid.UUID
		var creator string
		if err := tx.QueryRowContext(ctx, `SELECT template_id,created_by FROM notif_template_versions WHERE id=$1 AND status='pending_approval'`, id).Scan(&templateID, &creator); err != nil {
			return err
		}
		if creator == actor {
			return fmt.Errorf("template maker and checker must differ")
		}
		if _, err := tx.ExecContext(ctx, `UPDATE notif_template_versions SET status='retired',retired_at=now() WHERE template_id=$1 AND status='active'`, templateID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE notif_template_versions SET status='active',approved_by=$2,published_at=now() WHERE id=$1 AND status='pending_approval'`, id, actor)
		return err
	})
}
func (r *platformRepo) RejectTemplate(ctx context.Context, id uuid.UUID, actor, reason string) error {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return fmt.Errorf("rejection reason is required")
	}
	return r.db.WithTx(ctx, nil, func(tx *sql.Tx) error {
		var creator string
		if err := tx.QueryRowContext(ctx, `SELECT created_by FROM notif_template_versions WHERE id=$1 AND status='pending_approval'`, id).Scan(&creator); err != nil {
			return err
		}
		if creator == actor {
			return fmt.Errorf("template maker and checker must differ")
		}
		_, err := tx.ExecContext(ctx, `UPDATE notif_template_versions SET status='rejected',rejected_by=$2,rejection_reason=$3 WHERE id=$1 AND status='pending_approval'`, id, actor, reason)
		return err
	})
}
func (r *platformRepo) RetireTemplate(ctx context.Context, id uuid.UUID, actor string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE notif_template_versions SET status='retired',retired_at=now() WHERE id=$1 AND status='active'`, id)
	return err
}
