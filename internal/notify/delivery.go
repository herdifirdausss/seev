package notify

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/herdifirdausss/seev/internal/notify/channel"
	"github.com/herdifirdausss/seev/internal/notify/model"
	"github.com/herdifirdausss/seev/internal/notify/registry"
	"github.com/herdifirdausss/seev/pkg/cryptox"
	"github.com/herdifirdausss/seev/pkg/generalutil"
)

func (m *Module) startWorkers(ctx context.Context) {
	if m.platform == nil || (m.config.EmailSender == nil && m.config.PushSender == nil) {
		return
	}
	workerCtx, cancel := context.WithCancel(ctx)
	m.workersCancel = cancel
	for i := 0; i < max(1, m.config.EmailWorkers) && m.config.EmailSender != nil && m.config.EmailEnabled; i++ {
		go m.deliveryLoop(workerCtx, model.ChannelEmail, fmt.Sprintf("email-%d", i))
	}
	for i := 0; i < max(1, m.config.PushWorkers) && m.config.PushSender != nil && m.config.PushEnabled; i++ {
		go m.deliveryLoop(workerCtx, model.ChannelPush, fmt.Sprintf("push-%d", i))
	}
	if m.config.DigestEnabled && m.config.EmailEnabled && m.config.EmailSender != nil {
		for i := 0; i < max(1, m.config.DigestWorkers); i++ {
			go m.digestLoop(workerCtx, fmt.Sprintf("digest-%d", i))
		}
	}
}

func (m *Module) deliveryLoop(ctx context.Context, channelName, suffix string) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			owner := workerOwner(channelName, suffix)
			if err := m.processDue(ctx, channelName, owner); err != nil {
				m.logger.Error("notify: delivery worker tick failed", slog.String("channel", channelName), slog.Any("error", err))
			}
		}
	}
}

func workerOwner(channelName, suffix string) string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown"
	}
	return "notify/" + host + "/" + channelName + "/" + suffix
}

func (m *Module) processDue(ctx context.Context, channelName, owner string) error {
	defer m.refreshDeliveryHealth(ctx, channelName)
	control, err := m.platform.GetChannelControl(ctx, channelName)
	if err != nil {
		return err
	}
	observeNotificationChannelControl(control)
	if control.State == "paused" && (control.ExpiresAt == nil || control.ExpiresAt.After(time.Now().UTC())) {
		return nil
	}
	deliveries, err := m.platform.ClaimDue(ctx, channelName, m.config.DeliveryBatch, owner, time.Now().Add(m.config.DeliveryLease))
	if err != nil {
		return err
	}
	for _, delivery := range deliveries {
		if err := m.processDelivery(ctx, delivery, owner); err != nil {
			m.logger.Error("notify: delivery failed", slog.String("channel", channelName), slog.String("delivery_id", delivery.ID.String()), slog.Any("error", err))
		}
	}
	return nil
}

func (m *Module) refreshDeliveryHealth(ctx context.Context, channelName string) {
	if m.platform == nil {
		return
	}
	counts, oldest, err := m.platform.DeliveryHealth(ctx, channelName)
	if err != nil {
		m.logger.Warn("notify: delivery health lookup failed", slog.String("channel", channelName), slog.Any("error", err))
		return
	}
	for _, status := range []string{"pending_recipient", "scheduled", "retry_wait", "processing", "dead", "blocked"} {
		notificationDeliveryDueTotal.WithLabelValues(channelName, status).Set(float64(counts[status]))
	}
	age := 0.0
	if oldest != nil {
		age = time.Since(*oldest).Seconds()
		if age < 0 {
			age = 0
		}
	}
	notificationOldestDueAge.WithLabelValues(channelName).Set(age)
}

func (m *Module) markDeadDelivery(ctx context.Context, delivery model.Delivery, owner, code string) error {
	notificationDeadTotal.WithLabelValues(delivery.Channel, notificationMetricReason(code)).Inc()
	return m.platform.DeadDelivery(ctx, delivery.ID, owner, code)
}

func (m *Module) processDelivery(ctx context.Context, delivery model.Delivery, owner string) error {
	started := time.Now()
	result := channel.ProviderResult{}
	providerName := delivery.Channel
	var sendErr error
	var err error
	var notification model.Notification
	var kind registry.Kind
	if delivery.NotificationID != nil {
		notification, err = m.platform.GetNotification(ctx, delivery.UserID, *delivery.NotificationID)
		if err != nil {
			_ = m.markDeadDelivery(ctx, delivery, owner, "notification_missing")
			return err
		}
		var ok bool
		kind, ok = registry.Lookup(notification.Kind)
		if !ok {
			_ = m.markDeadDelivery(ctx, delivery, owner, "kind_unknown")
			return fmt.Errorf("unknown notification kind %q", notification.Kind)
		}
		preferences, prefErr := m.platform.ListPreferences(ctx, delivery.UserID)
		if prefErr != nil {
			return m.retryOrDead(ctx, delivery, owner, "preference_unavailable", prefErr)
		}
		mode := effectiveMode(kind, delivery.Channel, preferences, m.config)
		if mode == model.ModeDisabled || mode == model.ModeDailyDigest {
			return m.platform.SuppressDelivery(ctx, delivery.ID, owner, "preference_disabled")
		}
	}
	settings, err := m.platform.GetSettings(ctx, delivery.UserID)
	if err != nil {
		return m.retryOrDead(ctx, delivery, owner, "settings_unavailable", err)
	}
	settings = m.applySettingsDefaults(settings)
	due, err := NextAllowedTime(time.Now().UTC(), settings.Timezone, settings.QuietHoursEnabled, settings.QuietHoursStart, settings.QuietHoursEnd)
	if err != nil {
		return m.markDeadDelivery(ctx, delivery, owner, "timezone_invalid")
	}
	if due.After(time.Now().UTC().Add(time.Second)) {
		return m.platform.RetryDelivery(ctx, delivery.ID, owner, "quiet_hours", due)
	}

	switch delivery.Channel {
	case model.ChannelEmail:
		var recipient string
		if len(delivery.RecipientCiphertext) == 0 {
			if m.contact == nil {
				return m.retryOrDead(ctx, delivery, owner, "contact_resolver_unavailable", nil)
			}
			contactStarted := time.Now()
			recipient, verified, active, resolveErr := m.contact.Resolve(ctx, delivery.UserID.String())
			contactResult := "success"
			if resolveErr != nil {
				contactResult = "error"
			} else if !verified || !active || recipient == "" {
				contactResult = "unverified"
			}
			notificationContactResolutionTotal.WithLabelValues(contactResult).Inc()
			notificationContactResolutionDuration.WithLabelValues(contactResult).Observe(time.Since(contactStarted).Seconds())
			if resolveErr != nil {
				return m.retryOrDead(ctx, delivery, owner, "contact_resolution_failed", resolveErr)
			}
			if !verified || !active || recipient == "" {
				return m.platform.SuppressDelivery(ctx, delivery.ID, owner, "contact_unverified")
			}
			if m.config.EncryptionRing == nil {
				return m.markDeadDelivery(ctx, delivery, owner, "recipient_encryption_unavailable")
			}
			ciphertext, sealErr := m.config.EncryptionRing.Seal(cryptox.AAD{Service: "gateway", Table: "notif_deliveries", Column: "recipient", RowID: delivery.ID.String()}, []byte(recipient))
			if sealErr != nil {
				return m.retryOrDead(ctx, delivery, owner, "recipient_encrypt_failed", sealErr)
			}
			if err := m.platform.SetRecipientSnapshot(ctx, delivery.ID, owner, ciphertext, m.config.EncryptionRing.CurrentVersion(), m.config.fingerprint(recipient)); err != nil {
				return err
			}
			delivery.RecipientCiphertext = ciphertext
		}
		if m.config.EncryptionRing == nil {
			return m.markDeadDelivery(ctx, delivery, owner, "recipient_decryption_unavailable")
		}
		plain, openErr := m.config.EncryptionRing.Open(cryptox.AAD{Service: "gateway", Table: "notif_deliveries", Column: "recipient", RowID: delivery.ID.String()}, delivery.RecipientCiphertext)
		if openErr != nil {
			return m.markDeadDelivery(ctx, delivery, owner, "recipient_decrypt_failed")
		}
		recipient = string(plain)
		notificationID := ""
		if delivery.NotificationID != nil {
			notificationID = delivery.NotificationID.String()
		}
		message := channel.EmailMessage{DeliveryID: delivery.ID.String(), NotificationID: notificationID, To: recipient, Subject: delivery.RenderedSubject, Text: delivery.RenderedText, HTML: delivery.RenderedHTML, MessageID: delivery.ID.String() + "@seev.local"}
		result, sendErr = m.config.EmailSender.Send(ctx, message)
	case model.ChannelPush:
		if delivery.EndpointID == nil {
			return m.platform.SuppressDelivery(ctx, delivery.ID, owner, "device_missing")
		}
		device, deviceErr := m.platform.GetDevice(ctx, delivery.UserID, *delivery.EndpointID)
		if deviceErr != nil || device.Status != "active" {
			return m.platform.SuppressDelivery(ctx, delivery.ID, owner, "device_inactive")
		}
		if m.config.EncryptionRing == nil {
			return m.markDeadDelivery(ctx, delivery, owner, "token_decryption_unavailable")
		}
		plain, openErr := m.config.EncryptionRing.Open(cryptox.AAD{Service: "gateway", Table: "notif_device_endpoints", Column: "token", RowID: device.ID.String()}, device.TokenCiphertext)
		if openErr != nil {
			return m.markDeadDelivery(ctx, delivery, owner, "token_decrypt_failed")
		}
		message := channel.PushMessage{DeliveryID: delivery.ID.String(), NotificationID: notification.ID.String(), Token: string(plain), Platform: device.Platform, Title: delivery.RenderedTitle, Body: delivery.RenderedText, Data: map[string]string{"notification_id": notification.ID.String(), "kind": notification.Kind, "deep_link": notification.DeepLink}}
		result, sendErr = m.config.PushSender.Send(ctx, message)
	default:
		return m.markDeadDelivery(ctx, delivery, owner, "channel_unsupported")
	}

	finished := time.Now()
	attempt := model.DeliveryAttempt{ID: generalutil.NewV7(), DeliveryID: delivery.ID, AttemptNumber: delivery.AttemptCount, LeaseOwner: owner, Provider: providerName, StartedAt: started, FinishedAt: &finished, Result: "failed", StatusClass: "transport", ProviderMessageID: result.ProviderMessageID, ErrorCode: result.ErrorCode, DurationMS: int(finished.Sub(started).Milliseconds()), ResponseExcerpt: result.ResponseExcerpt}
	if result.Accepted {
		attempt.Result = "accepted"
		attempt.StatusClass = "2xx"
	}
	if sendErr != nil && result.ErrorCode == "" {
		attempt.ErrorCode = "provider_error"
	}
	if err := m.platform.InsertAttempt(ctx, attempt); err != nil {
		return err
	}
	attemptResult := "failed"
	if result.Accepted {
		attemptResult = "accepted"
	} else if result.InvalidEndpoint {
		attemptResult = "invalid_endpoint"
	}
	notificationDeliveryAttemptsTotal.WithLabelValues(delivery.Channel, providerName, attemptResult).Inc()
	notificationDeliveryDuration.WithLabelValues(delivery.Channel, providerName, attemptResult).Observe(finished.Sub(started).Seconds())
	kindName := model.KindDailyDigest
	if notification.Kind != "" {
		kindName = notification.Kind
	}
	if result.Accepted {
		notificationDeliveriesTotal.WithLabelValues(delivery.Channel, kindName, "delivered").Inc()
		return m.platform.FinishDelivery(ctx, delivery.ID, owner, result.ProviderMessageID)
	}
	if result.InvalidEndpoint && delivery.EndpointID != nil {
		_ = m.platform.MarkDeviceInvalid(ctx, *delivery.EndpointID, result.ErrorCode)
	}
	if result.Permanent {
		notificationDeliveriesTotal.WithLabelValues(delivery.Channel, kindName, "dead").Inc()
		return m.markDeadDelivery(ctx, delivery, owner, firstNonEmpty(result.ErrorCode, "provider_error"))
	}
	delays := emailRetrySchedule
	if delivery.Channel == model.ChannelPush {
		delays = pushRetrySchedule
	}
	if delivery.AttemptCount >= len(delays) {
		notificationDeliveriesTotal.WithLabelValues(delivery.Channel, kindName, "dead").Inc()
	} else {
		notificationDeliveriesTotal.WithLabelValues(delivery.Channel, kindName, "retry_wait").Inc()
	}
	return m.retryOrDead(ctx, delivery, owner, firstNonEmpty(result.ErrorCode, "provider_transient"), sendErr)
}

func (m *Module) retryOrDead(ctx context.Context, delivery model.Delivery, owner, code string, cause error) error {
	delays := emailRetrySchedule
	if delivery.Channel == model.ChannelPush {
		delays = pushRetrySchedule
	}
	if delivery.AttemptCount >= len(delays) {
		return m.markDeadDelivery(ctx, delivery, owner, code)
	}
	return m.platform.RetryDelivery(ctx, delivery.ID, owner, code, time.Now().Add(delays[delivery.AttemptCount]))
}

var emailRetrySchedule = []time.Duration{0, time.Minute, 5 * time.Minute, 30 * time.Minute, 2 * time.Hour, 8 * time.Hour, 24 * time.Hour}
var pushRetrySchedule = []time.Duration{0, 30 * time.Second, 2 * time.Minute, 10 * time.Minute, time.Hour}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return "unknown"
}

func (m *Module) digestLoop(ctx context.Context, suffix string) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			owner := workerOwner(model.ChannelEmail, suffix)
			control, controlErr := m.platform.GetChannelControl(ctx, "digest")
			if controlErr != nil {
				m.logger.Error("notify: digest control lookup failed", slog.Any("error", controlErr))
				continue
			}
			observeNotificationChannelControl(control)
			if control.State == "paused" && (control.ExpiresAt == nil || control.ExpiresAt.After(time.Now().UTC())) {
				continue
			}
			windows, err := m.platform.ClaimDigestWindows(ctx, m.config.DigestWorkers, owner, time.Now().UTC().Add(m.config.DeliveryLease))
			if err != nil {
				m.logger.Error("notify: digest claim failed", slog.Any("error", err))
				continue
			}
			for _, window := range windows {
				if err := m.processDigestWindow(ctx, window, owner); err != nil {
					m.logger.Error("notify: digest window failed", slog.String("window_id", window.ID.String()), slog.Any("error", err))
				}
			}
		}
	}
}

func (m *Module) processDigestWindow(ctx context.Context, window model.DigestWindow, owner string) error {
	lag := time.Since(window.ScheduledAt).Seconds()
	if lag < 0 {
		lag = 0
	}
	notificationDigestScheduleLag.Set(lag)
	items, err := m.platform.ListDigestNotifications(ctx, window.ID, window.UserID)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		notificationDigestWindowsTotal.WithLabelValues("empty_suppressed").Inc()
		return m.platform.FinishDigestWindow(ctx, window.ID, owner, "suppressed")
	}
	const maxSourceItems = 100
	const maxRenderedItems = 20
	if len(items) > maxSourceItems {
		items = items[:maxSourceItems]
	}
	shown := items
	if len(shown) > maxRenderedItems {
		shown = shown[:maxRenderedItems]
	}
	sourceCount := min(window.ItemCount, maxSourceItems)
	moreCount := max(sourceCount-len(shown), 0)
	digestContext := model.DigestRenderContext{
		WindowDate: window.LocalWindowDate.Format("2006-01-02"),
		Items:      make([]model.DigestItemContext, 0, len(shown)),
		MoreCount:  moreCount,
	}
	for _, item := range shown {
		digestContext.Items = append(digestContext.Items, model.DigestItemContext{
			Title: item.Title, Body: item.Body, DeepLink: item.DeepLink,
		})
	}
	version, ok, err := m.activeTemplate(ctx, model.KindDailyDigest, model.ChannelEmail, window.Locale)
	if err != nil {
		return fmt.Errorf("load daily digest template: %w", err)
	}
	if !ok {
		notificationTemplateMissingTotal.WithLabelValues(model.ChannelEmail, model.KindDailyDigest, window.Locale).Inc()
		notificationBlockedTotal.WithLabelValues(model.ChannelEmail, "template_missing").Inc()
		notificationDigestWindowsTotal.WithLabelValues("missing_template").Inc()
		return m.platform.FinishDigestWindow(ctx, window.ID, owner, "dead")
	}
	rendered, err := m.renderer.RenderDigest(version, digestContext)
	if err != nil {
		notificationTemplateRenderTotal.WithLabelValues(model.ChannelEmail, model.KindDailyDigest, window.Locale, "error").Inc()
		notificationDigestWindowsTotal.WithLabelValues("render_error").Inc()
		return m.platform.FinishDigestWindow(ctx, window.ID, owner, "dead")
	}
	notificationTemplateRenderTotal.WithLabelValues(model.ChannelEmail, model.KindDailyDigest, window.Locale, "success").Inc()
	if err := m.platform.CreateDigestDelivery(ctx, window, rendered.Subject, rendered.Text, rendered.HTML, version.ID, rendered.Hash); err != nil {
		return err
	}
	notificationDigestWindowsTotal.WithLabelValues("planned").Inc()
	return nil
}
