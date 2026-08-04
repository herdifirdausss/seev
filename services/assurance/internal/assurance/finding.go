package assurance

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Finding is the persistence-safe representation used by the rule engine.
type Finding struct {
	Fingerprint string
	Severity    string
	RuleCode    string
	ResourceID  string
	AmountMinor int64
	Currency    string
	Evidence    map[string]string
}

func (m *Module) UpsertFinding(ctx context.Context, finding Finding, seenAt time.Time) error {
	_, err := m.upsertFinding(ctx, finding, seenAt, false)
	return err
}

func (m *Module) resolveResourceFindings(ctx context.Context, resourceID string, seen map[string]bool) error {
	rows, err := m.db.QueryContext(ctx, `SELECT id, fingerprint FROM assurance_findings WHERE resource_id=$1 AND status IN ('open','acknowledged')`, resourceID)
	if err != nil {
		return err
	}
	defer rows.Close()
	type findingRef struct {
		id          uuid.UUID
		fingerprint string
	}
	var refs []findingRef
	for rows.Next() {
		var ref findingRef
		if err := rows.Scan(&ref.id, &ref.fingerprint); err != nil {
			return err
		}
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, ref := range refs {
		if seen[ref.fingerprint] {
			continue
		}
		if _, err := m.db.ExecContext(ctx, `UPDATE assurance_findings SET status='resolved', resolved_at=now() WHERE id=$1 AND status IN ('open','acknowledged')`, ref.id); err != nil {
			return err
		}
	}
	return nil
}

func (m *Module) upsertFinding(ctx context.Context, finding Finding, seenAt time.Time, suppressAlert bool) (bool, error) {
	if finding.Fingerprint == "" || finding.RuleCode == "" || finding.ResourceID == "" {
		return false, errors.New("finding fingerprint, rule code, and resource id are required")
	}
	evidence, err := json.Marshal(finding.Evidence)
	if err != nil {
		return false, fmt.Errorf("marshal finding evidence: %w", err)
	}
	var existingID uuid.UUID
	var existingStatus, existingSeverity string
	var existingBaseline bool
	existingErr := m.db.QueryRowContext(ctx, `SELECT id, status, severity, baseline FROM assurance_findings WHERE fingerprint=$1`, finding.Fingerprint).Scan(&existingID, &existingStatus, &existingSeverity, &existingBaseline)
	if existingErr != nil && !errors.Is(existingErr, sql.ErrNoRows) {
		return false, fmt.Errorf("read finding state: %w", existingErr)
	}
	isNew := errors.Is(existingErr, sql.ErrNoRows)
	findingID := existingID
	if isNew {
		findingID = uuid.New()
	}
	_, err = m.db.ExecContext(ctx, `INSERT INTO assurance_findings (id, fingerprint, severity, rule_code, resource_id, amount_minor, currency, evidence, first_seen_at, last_seen_at, occurrence_count, status, baseline) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$9,1,'open',$10) ON CONFLICT (fingerprint) DO UPDATE SET severity=EXCLUDED.severity, amount_minor=EXCLUDED.amount_minor, currency=EXCLUDED.currency, evidence=EXCLUDED.evidence, last_seen_at=EXCLUDED.last_seen_at, occurrence_count=assurance_findings.occurrence_count+1, status=CASE WHEN assurance_findings.status='resolved' THEN 'open' ELSE assurance_findings.status END, resolved_at=CASE WHEN assurance_findings.status='resolved' THEN NULL ELSE assurance_findings.resolved_at END, baseline=EXCLUDED.baseline`, findingID, finding.Fingerprint, finding.Severity, finding.RuleCode, finding.ResourceID, finding.AmountMinor, finding.Currency, evidence, seenAt, suppressAlert)
	if err != nil {
		return false, err
	}
	shouldAlert := !suppressAlert && (isNew || existingStatus == "resolved" || existingBaseline || severityRank(finding.Severity) > severityRank(existingSeverity))
	if shouldAlert {
		message := fmt.Sprintf("assurance finding %s rule=%s resource=%s amount=%d currency=%s", finding.Severity, finding.RuleCode, finding.ResourceID, finding.AmountMinor, finding.Currency)
		if _, err := m.db.ExecContext(ctx, `INSERT INTO assurance_alert_deliveries (id, finding_id, severity, message, status) VALUES ($1,$2,$3,$4,'pending')`, uuid.New(), findingID, finding.Severity, message); err != nil {
			return false, fmt.Errorf("queue assurance alert: %w", err)
		}
	}
	return shouldAlert, nil
}

func severityRank(severity string) int {
	switch severity {
	case "critical":
		return 3
	case "high":
		return 2
	case "medium":
		return 1
	default:
		return 0
	}
}
