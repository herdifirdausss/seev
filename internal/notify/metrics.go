package notify

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/herdifirdausss/seev/internal/notify/model"
)

// Notification metrics deliberately use only bounded policy values. User,
// event, delivery, device, address, and provider-error identifiers never
// become labels.
var (
	notificationEventsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "seev", Subsystem: "notification", Name: "events_total",
		Help: "Notification source events handled by result.",
	}, []string{"source", "event_type", "result"})
	notificationEventDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "seev", Subsystem: "notification", Name: "event_processing_duration_seconds",
		Help: "Notification event processing duration.",
	}, []string{"source", "event_type", "result"})
	notificationEventsFilteredTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "seev", Subsystem: "notification", Name: "events_filtered_total",
		Help: "Valid source events filtered without a user-facing notification.",
	}, []string{"source", "reason"})
	notificationLogicalCreatedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "seev", Subsystem: "notification", Name: "logical_created_total",
		Help: "Logical notifications created by kind and category.",
	}, []string{"kind", "category"})
	notificationDuplicatesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "seev", Subsystem: "notification", Name: "duplicates_total",
		Help: "Duplicate logical notification plans discarded by the uniqueness guard.",
	}, []string{"source", "event_type"})
	notificationPlanningFailuresTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "seev", Subsystem: "notification", Name: "planning_failures_total",
		Help: "Notification planning failures by bounded policy reason.",
	}, []string{"kind", "reason"})
	notificationDeliveriesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "seev", Subsystem: "notification", Name: "deliveries_total",
		Help: "Durable channel delivery outcomes.",
	}, []string{"channel", "kind", "result"})
	notificationDeliveryAttemptsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "seev", Subsystem: "notification", Name: "delivery_attempts_total",
		Help: "Provider delivery attempts by bounded result.",
	}, []string{"channel", "provider", "result"})
	notificationDeliveryDueTotal = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "seev", Subsystem: "notification", Name: "delivery_due_total",
		Help: "Current notification deliveries eligible for a provider attempt, by channel and status.",
	}, []string{"channel", "status"})
	notificationOldestDueAge = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "seev", Subsystem: "notification", Name: "oldest_due_age_seconds",
		Help: "Age of the oldest notification delivery waiting for a provider attempt.",
	}, []string{"channel"})
	notificationDeadTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "seev", Subsystem: "notification", Name: "dead_total",
		Help: "Notification deliveries moved to dead by bounded reason.",
	}, []string{"channel", "reason"})
	notificationBlockedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "seev", Subsystem: "notification", Name: "blocked_total",
		Help: "Notification deliveries blocked by bounded reason.",
	}, []string{"channel", "reason"})
	notificationDeliveryDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "seev", Subsystem: "notification", Name: "delivery_duration_seconds",
		Help: "Provider delivery duration.",
	}, []string{"channel", "provider", "result"})
	notificationContactResolutionTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "seev", Subsystem: "notification", Name: "contact_resolution_total",
		Help: "Auth contact resolution outcomes.",
	}, []string{"result"})
	notificationContactResolutionDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "seev", Subsystem: "notification", Name: "contact_resolution_duration_seconds",
		Help: "Auth contact resolution duration.",
	}, []string{"result"})
	notificationPreferenceUpdatesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "seev", Subsystem: "notification", Name: "preference_updates_total",
		Help: "Notification preference update outcomes by bounded channel and mode.",
	}, []string{"channel", "mode", "result"})
	notificationTemplateRenderTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "seev", Subsystem: "notification", Name: "template_render_total",
		Help: "Notification template render outcomes.",
	}, []string{"channel", "kind", "locale", "result"})
	notificationTemplateMissingTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "seev", Subsystem: "notification", Name: "template_missing_total",
		Help: "Missing active notification templates.",
	}, []string{"channel", "kind", "locale"})
	notificationTemplatePublishTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "seev", Subsystem: "notification", Name: "template_publish_total",
		Help: "Notification template publication outcomes.",
	}, []string{"channel", "kind", "result"})
	notificationDigestWindowsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "seev", Subsystem: "notification", Name: "digest_windows_total",
		Help: "Daily digest window outcomes.",
	}, []string{"result"})
	notificationDigestItemsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "seev", Subsystem: "notification", Name: "digest_items_total",
		Help: "Notifications included in daily digest planning.",
	}, []string{"category"})
	notificationDigestScheduleLag = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "seev", Subsystem: "notification", Name: "digest_schedule_lag_seconds",
		Help: "Lag between the oldest claimed daily digest schedule and the current time.",
	})
	notificationChannelState = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "seev", Subsystem: "notification", Name: "channel_state",
		Help: "Current notification channel control state; exactly one state is 1 per channel.",
	}, []string{"channel", "state"})
	notificationDevicesTotal = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "seev", Subsystem: "notification", Name: "devices",
		Help: "Observed push device endpoint state transitions.",
	}, []string{"platform", "status"})
)

func observeNotificationChannelControl(control model.ChannelControl) {
	for _, state := range []string{"running", "paused", "drain_only"} {
		value := 0.0
		if state == control.State {
			value = 1
		}
		notificationChannelState.WithLabelValues(control.Channel, state).Set(value)
	}
}

// Provider and database errors are reduced to a small stable vocabulary before
// they become metric labels. The original code remains in the durable delivery
// row for operator inspection, but never expands Prometheus cardinality.
func notificationMetricReason(value string) string {
	switch value {
	case "kind_unknown", "notification_missing", "timezone_invalid", "recipient_encryption_unavailable",
		"recipient_decryption_unavailable", "recipient_decrypt_failed", "token_decryption_unavailable",
		"token_decrypt_failed", "channel_unsupported", "contact_resolver_unavailable", "contact_resolution_failed",
		"contact_unverified", "settings_unavailable", "preference_unavailable", "device_missing", "device_inactive",
		"provider_transient", "push_invalid_token", "push_rate_limited", "smtp_permanent", "template_missing",
		"template_render_failed", "provider_error":
		return value
	default:
		return "other"
	}
}
