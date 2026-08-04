package assurance

import (
	"context"
	"time"

	"github.com/google/uuid"
)

func (m *Module) dispatchAlerts(ctx context.Context) error {
	if m.alertFn == nil {
		return nil
	}
	rows, err := m.db.QueryContext(ctx, `SELECT id, severity, message, attempts FROM assurance_alert_deliveries WHERE status='pending' AND next_attempt_at <= now() ORDER BY created_at, id LIMIT 50`)
	if err != nil {
		return err
	}
	defer rows.Close()
	type delivery struct {
		id       uuid.UUID
		severity string
		message  string
		attempts int
	}
	var deliveries []delivery
	for rows.Next() {
		var item delivery
		if err := rows.Scan(&item.id, &item.severity, &item.message, &item.attempts); err != nil {
			return err
		}
		deliveries = append(deliveries, item)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, item := range deliveries {
		if err := m.alertFn(ctx, item.severity, item.message); err != nil {
			alertDeliveries.WithLabelValues("failed", item.severity).Inc()
			backoff := time.Duration(1<<min(item.attempts, 6)) * time.Minute
			_, _ = m.db.ExecContext(ctx, `UPDATE assurance_alert_deliveries SET status='pending', attempts=attempts+1, next_attempt_at=now()+($2 * interval '1 second'), last_error=$3 WHERE id=$1`, item.id, backoff.Seconds(), err.Error())
			continue
		}
		alertDeliveries.WithLabelValues("delivered", item.severity).Inc()
		_, _ = m.db.ExecContext(ctx, `UPDATE assurance_alert_deliveries SET status='delivered', attempts=attempts+1, delivered_at=now(), last_error='' WHERE id=$1`, item.id)
	}
	return nil
}
