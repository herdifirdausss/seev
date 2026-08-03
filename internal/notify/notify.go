// Package notify is the public facade for the in-app notification inbox
// (docs/roadmap/archive/25 Task T4) — the first RabbitMQ CONSUMER in this codebase
// (every other module only publishes to the outbox). External code may
// only import this package; internal/notify/repository and
// internal/notify/model are private to the module (docs/roadmap/archive/01 Module
// Boundaries, enforced by boundary_test.go). The ONLY internal/ledger
// subpackage this module imports is internal/ledger/events — the
// versioned outbox payload contract any consumer may decode.
package notify

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/shopspring/decimal"

	"github.com/herdifirdausss/seev/internal/ledger/events"
	"github.com/herdifirdausss/seev/internal/notify/channel"
	"github.com/herdifirdausss/seev/internal/notify/model"
	"github.com/herdifirdausss/seev/internal/notify/registry"
	"github.com/herdifirdausss/seev/internal/notify/repository"
	notifytemplate "github.com/herdifirdausss/seev/internal/notify/template"
	currencyreg "github.com/herdifirdausss/seev/pkg/currency"
	"github.com/herdifirdausss/seev/pkg/database"
	"github.com/herdifirdausss/seev/pkg/generalutil"
	"github.com/herdifirdausss/seev/pkg/messaging"
)

// Notification is re-exported so callers never need to import
// internal/notify/model.
type Notification = model.Notification

const queueName = "ledger.events.notifications"
const consumerTag = "notify-consumer"

// notifiableTypes are the ledger transaction_type values that produce a
// user-facing notification (docs/roadmap/archive/25 Task T4 step 3). Every other
// TransactionPosted event is filtered out silently — acked, no row
// written.
var notifiableTypes = map[string]bool{
	"money_in":        true,
	"transfer_p2p":    true,
	"withdraw_settle": true,
	"withdraw_cancel": true,
}

// Broker is the subset of messaging.Broker the notify module depends on —
// a local structural interface (mirrors internal/payin's Poster pattern)
// so unit tests can inject a mock without a real AMQP connection.
type Broker interface {
	messaging.Consumer
	messaging.TopologyManager
}

// Module is the notify module's public facade.
type Module struct {
	db            database.DatabaseSQL
	repo          repository.Repository
	platform      repository.PlatformRepository
	broker        Broker
	logger        *slog.Logger
	config        Config
	renderer      *notifytemplate.Renderer
	contact       ContactResolver
	cancel        context.CancelFunc
	workersCancel context.CancelFunc
}

func NewModule(db database.DatabaseSQL, broker Broker, logger *slog.Logger) *Module {
	if logger == nil {
		logger = slog.Default()
	}
	return &Module{
		db: db, repo: repository.NewRepository(db), platform: repository.NewPlatformRepository(db), broker: broker,
		logger: logger, config: DefaultConfig(), renderer: notifytemplate.NewRenderer(0, 0),
	}
}

// NewConfiguredModule wires C3's optional external channels. The old
// NewModule constructor remains valid for callers that only need the inbox.
func NewConfiguredModule(db database.DatabaseSQL, broker Broker, cfg Config, logger *slog.Logger, contactResolver ContactResolver, emailSender channel.EmailSender, pushSender channel.PushSender) *Module {
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.DefaultLocale == "" {
		cfg.DefaultLocale = "en-US"
	}
	if cfg.DefaultTimezone == "" {
		cfg.DefaultTimezone = "Asia/Jakarta"
	}
	if cfg.DeliveryBatch <= 0 {
		cfg.DeliveryBatch = 50
	}
	if cfg.DeliveryLease <= 0 {
		cfg.DeliveryLease = 2 * time.Minute
	}
	if cfg.EventPrefetch <= 0 {
		cfg.EventPrefetch = 10
	}
	if cfg.MaxEventAttempts <= 0 {
		cfg.MaxEventAttempts = 5
	}
	cfg.EmailSender, cfg.PushSender = emailSender, pushSender
	return &Module{db: db, repo: repository.NewRepository(db), platform: repository.NewPlatformRepository(db), broker: broker, logger: logger, config: cfg, renderer: notifytemplate.NewRenderer(0, 0), contact: contactResolver}
}

// Start declares the queue topology, then launches the consumer in its own
// goroutine (docs/roadmap/archive/25 Task T4 step 3). Returns an error only if
// topology declaration itself fails; the consumer goroutine's own errors
// are logged, not returned (it self-heals via messaging.RabbitMQ.Consume's
// built-in reconnect/backoff loop).
func (m *Module) Start(ctx context.Context) error {
	if m.broker == nil {
		return fmt.Errorf("notify: broker is not configured")
	}
	if err := m.broker.DeclareTopology(ctx, messaging.QueueConfig{
		Queue:       queueName,
		RoutingKeys: []string{events.TypeTransactionPosted, events.TypeFXConversionPosted},
	}); err != nil {
		return fmt.Errorf("notify: declare topology: %w", err)
	}

	consumeCtx, cancel := context.WithCancel(ctx)
	m.cancel = cancel

	go func() {
		if err := m.broker.Consume(consumeCtx, messaging.ConsumeOptions{
			Queue:               queueName,
			ConsumerTag:         consumerTag,
			PrefetchCount:       m.config.EventPrefetch,
			MaxDeliveryAttempts: m.config.MaxEventAttempts,
		}, m.handleDelivery); err != nil {
			m.logger.Error("notify: consumer stopped", "error", err)
		}
	}()
	m.startWorkers(consumeCtx)
	return nil
}

// Stop cancels the consumer goroutine. Safe to call even if Start was
// never called or failed — cancel is nil-checked.
func (m *Module) Stop() {
	if m.cancel != nil {
		m.cancel()
	}
	if m.workersCancel != nil {
		m.workersCancel()
	}
}

// handleDelivery is the messaging.HandlerFunc bound to queueName: decode →
// filter → fan out to recipient(s) → dedup-insert. Every returned error is
// a plain (non-Retriable) error — a malformed payload or a permanent
// decode failure will never succeed differently on redelivery, so it
// routes straight to the DLQ rather than being requeued forever. A
// transient DB error also returns plain (not Retriable): RabbitMQ's own
// redelivery-on-first-nack behavior (pkg/messaging's shouldRequeue: retry
// once, then DLQ on redelivery) already gives one free retry without this
// handler needing to distinguish transient from permanent DB failures
// itself.
func (m *Module) handleDelivery(ctx context.Context, d amqp.Delivery) error {
	if d.RoutingKey == events.TypeFXConversionPosted {
		return m.handleFXConversionDelivery(ctx, d)
	}

	started := time.Now()
	result := "error"
	defer func() {
		notificationEventsTotal.WithLabelValues("ledger", events.TypeTransactionPosted, result).Inc()
		notificationEventDuration.WithLabelValues("ledger", events.TypeTransactionPosted, result).Observe(time.Since(started).Seconds())
	}()
	var ev events.TransactionPosted
	if err := json.Unmarshal(d.Body, &ev); err != nil {
		return fmt.Errorf("notify: decode TransactionPosted: %w", err)
	}
	if err := ev.Validate(); err != nil {
		return fmt.Errorf("notify: validate TransactionPosted: %w", err)
	}

	if !notifiableTypes[ev.TransactionType] {
		result = "filtered"
		notificationEventsFilteredTotal.WithLabelValues("ledger", "transaction_type").Inc()
		return nil
	}

	var eventID uuid.UUID
	if ev.EventID != nil {
		eventID = *ev.EventID
	} else {
		var err error
		eventID, err = uuid.Parse(d.MessageId)
		if err != nil {
			return fmt.Errorf("notify: invalid message id %q: %w", d.MessageId, err)
		}
	}
	if m.platform != nil && m.db != nil {
		err := m.handleModernDelivery(ctx, d, ev, eventID)
		if err == nil {
			result = "processed"
		}
		return err
	}

	for _, rcpt := range recipientsFor(ev) {
		n := model.Notification{
			ID:      generalutil.NewV7(),
			UserID:  rcpt.userID,
			EventID: eventID,
			Type:    ev.TransactionType,
			Title:   rcpt.title,
			Body:    rcpt.body,
			Payload: d.Body,
		}
		if _, err := m.repo.Insert(ctx, n); err != nil {
			return fmt.Errorf("notify: insert notification: %w", err)
		}
	}
	result = "processed"
	return nil
}

func (m *Module) handleFXConversionDelivery(ctx context.Context, d amqp.Delivery) error {
	var ev events.FXConversionPosted
	if err := json.Unmarshal(d.Body, &ev); err != nil {
		return fmt.Errorf("notify: decode FXConversionPosted: %w", err)
	}
	if err := ev.Validate(); err != nil {
		return fmt.Errorf("notify: validate FXConversionPosted: %w", err)
	}

	body := fmt.Sprintf("Your %s %s conversion to %s %s was completed.",
		ev.SourceCurrency, formatMinorAmount(ev.SourceCurrency, ev.SourceAmount, ev.SourceMinorUnit),
		ev.TargetCurrency, formatMinorAmount(ev.TargetCurrency, ev.TargetAmount, ev.TargetMinorUnit))
	n := model.Notification{
		ID:      generalutil.NewV7(),
		UserID:  ev.UserID,
		EventID: *ev.EventID,
		Type:    events.TypeFXConversionPosted,
		Title:   "Currency conversion completed",
		Body:    body,
		Payload: d.Body,
	}
	if _, err := m.repo.Insert(ctx, n); err != nil {
		return fmt.Errorf("notify: insert FX conversion notification: %w", err)
	}
	return nil
}

type modernRecipient struct {
	userID uuid.UUID
	role   string
}

func modernRecipientsFor(ev events.TransactionPosted) []modernRecipient {
	if ev.TransactionType == "transfer_p2p" {
		out := make([]modernRecipient, 0, 2)
		if ev.UserID != nil {
			out = append(out, modernRecipient{userID: *ev.UserID, role: "sender"})
		}
		if ev.TargetUserID != nil {
			out = append(out, modernRecipient{userID: *ev.TargetUserID, role: "receiver"})
		}
		return out
	}
	if ev.UserID == nil {
		return nil
	}
	return []modernRecipient{{userID: *ev.UserID, role: "owner"}}
}

func (m *Module) handleModernDelivery(ctx context.Context, d amqp.Delivery, ev events.TransactionPosted, eventID uuid.UUID) (err error) {
	if !m.config.Enabled || !m.config.InAppEnabled {
		return nil
	}
	failureInbox := model.EventInbox{ID: generalutil.NewV7(), SourceService: "ledger", EventID: eventID,
		EventType: events.TypeTransactionPosted, SchemaVersion: ev.SchemaVersion, PayloadHash: sha256Bytes(d.Body),
		Status: "failed", ReceivedAt: time.Now().UTC()}
	defer func() {
		if err == nil {
			return
		}
		notificationPlanningFailuresTotal.WithLabelValues("unknown", "planner_error").Inc()
		if recordErr := m.platform.RecordEventFailure(ctx, failureInbox, "planner_error"); recordErr != nil {
			m.logger.Warn("notify: record planning failure failed", slog.Any("error", recordErr))
		}
	}()
	for _, recipient := range modernRecipientsFor(ev) {
		kind, err := registry.KindForTransaction(ev.TransactionType, recipient.role)
		if err != nil {
			return err
		}
		notificationID := generalutil.NewV7()
		deepLink := strings.ReplaceAll(kind.DeepLinkPath, "{id}", ev.TxID.String())
		renderContext := model.RenderContext{
			NotificationID: notificationID.String(),
			Amount:         model.MoneyContext{Minor: ev.Amount, Currency: ev.Currency},
			Transaction:    model.TransactionContext{ID: ev.TxID.String(), PostedAt: ev.OccurredAt},
			Action:         model.ActionContext{DeepLink: deepLink},
		}
		renderContext.Amount.Display = notifytemplate.FormatMoney(renderContext.Amount)
		contextJSON, err := json.Marshal(renderContext)
		if err != nil {
			return fmt.Errorf("notify: encode render context: %w", err)
		}
		inAppVersion, ok, err := m.activeTemplate(ctx, kind.Kind, model.ChannelInApp, m.config.DefaultLocale)
		if err != nil {
			return fmt.Errorf("notify: load in-app template %s: %w", kind.Kind, err)
		}
		if !ok {
			notificationTemplateMissingTotal.WithLabelValues(model.ChannelInApp, kind.Kind, m.config.DefaultLocale).Inc()
			notificationPlanningFailuresTotal.WithLabelValues(kind.Kind, "in_app_template_missing").Inc()
			return fmt.Errorf("notify: missing in-app template for %s", kind.Kind)
		}
		inApp, err := m.renderer.Render(inAppVersion, renderContext)
		if err != nil {
			notificationTemplateRenderTotal.WithLabelValues(model.ChannelInApp, kind.Kind, m.config.DefaultLocale, "error").Inc()
			return fmt.Errorf("notify: render in-app %s: %w", kind.Kind, err)
		}
		notificationTemplateRenderTotal.WithLabelValues(model.ChannelInApp, kind.Kind, m.config.DefaultLocale, "success").Inc()
		contentHash := sha256.Sum256(inApp.Payload)
		notification := model.Notification{
			ID: notificationID, UserID: recipient.userID, EventID: eventID, Type: ev.TransactionType,
			Title: inApp.Title, Body: inApp.Text, Payload: []byte(`{}`), EventType: events.TypeTransactionPosted,
			SourceService: "ledger", Kind: kind.Kind, Category: kind.Category, Priority: kind.Priority,
			Requirement: kind.Requirement, Locale: m.config.DefaultLocale, TemplateVersionID: &inAppVersion.ID,
			DeepLink: deepLink, Context: contextJSON, ContentHash: contentHash[:], CreatedAt: time.Now().UTC(),
		}
		deliveries := []model.Delivery{{
			ID: generalutil.NewV7(), NotificationID: &notification.ID, UserID: notification.UserID,
			Channel: model.ChannelInApp, Status: model.DeliveryDelivered, TemplateVersionID: inAppVersion.ID,
			Locale: notification.Locale, RenderedTitle: inApp.Title, RenderedText: inApp.Text,
			ContentHash: inApp.Hash, CreatedAt: time.Now().UTC(),
		}}
		settings, err := m.platform.GetSettings(ctx, recipient.userID)
		if err != nil {
			return fmt.Errorf("notify: load settings: %w", err)
		}
		settings = m.applySettingsDefaults(settings)
		preferences, err := m.platform.ListPreferences(ctx, recipient.userID)
		if err != nil {
			return fmt.Errorf("notify: load preferences: %w", err)
		}
		digestItems := make([]model.DigestRequest, 0, 1)
		for _, channelName := range []string{model.ChannelEmail, model.ChannelPush} {
			mode := effectiveMode(kind, channelName, preferences, m.config)
			if mode == model.ModeDisabled {
				continue
			}
			if mode == model.ModeDailyDigest {
				if channelName != model.ChannelEmail || !kind.DigestEligible || !m.config.DigestEnabled {
					continue
				}
				allowed, controlErr := m.channelAllowsPlanning(ctx, "digest")
				if controlErr != nil {
					return controlErr
				}
				if !allowed {
					continue
				}
				request, requestErr := newDigestRequest(time.Now().UTC(), notification.ID, notification.UserID, notification.Locale, settings)
				if requestErr != nil {
					return requestErr
				}
				digestItems = append(digestItems, request)
				continue
			}
			allowed, controlErr := m.channelAllowsPlanning(ctx, channelName)
			if controlErr != nil {
				return controlErr
			}
			if !allowed {
				continue
			}
			due, err := NextAllowedTime(time.Now().UTC(), settings.Timezone, settings.QuietHoursEnabled, settings.QuietHoursStart, settings.QuietHoursEnd)
			if err != nil {
				return err
			}
			version, ok, err := m.activeTemplate(ctx, kind.Kind, channelName, notification.Locale)
			if err != nil {
				return fmt.Errorf("notify: load %s template %s: %w", channelName, kind.Kind, err)
			}
			if !ok {
				notificationTemplateMissingTotal.WithLabelValues(channelName, kind.Kind, notification.Locale).Inc()
				notificationBlockedTotal.WithLabelValues(channelName, "template_missing").Inc()
				// Keep the in-app record and make the missing external template
				// visible to operators without creating a provider-callable row.
				if channelName == model.ChannelPush {
					devices, deviceErr := m.platform.ListActiveDevices(ctx, recipient.userID)
					if deviceErr != nil {
						return fmt.Errorf("notify: list active devices for blocked push: %w", deviceErr)
					}
					for _, device := range devices {
						endpointID := device.ID
						deliveries = append(deliveries, model.Delivery{
							ID: generalutil.NewV7(), NotificationID: &notification.ID, UserID: notification.UserID,
							Channel: channelName, EndpointID: &endpointID, EndpointIdentity: device.ID.String(), Status: model.DeliveryBlocked,
							TemplateVersionID: uuid.Nil, Locale: notification.Locale, RenderedText: "",
							ContentHash: sha256Bytes(nil), CreatedAt: time.Now().UTC(),
						})
						notificationDeliveriesTotal.WithLabelValues(channelName, kind.Kind, "blocked").Inc()
					}
				} else {
					deliveries = append(deliveries, model.Delivery{
						ID: generalutil.NewV7(), NotificationID: &notification.ID, UserID: notification.UserID,
						Channel: channelName, EndpointIdentity: "template-missing", Status: model.DeliveryBlocked,
						TemplateVersionID: uuid.Nil, Locale: notification.Locale, RenderedText: "",
						ContentHash: sha256Bytes(nil), CreatedAt: time.Now().UTC(),
					})
					notificationDeliveriesTotal.WithLabelValues(channelName, kind.Kind, "blocked").Inc()
				}
				notificationPlanningFailuresTotal.WithLabelValues(kind.Kind, "external_template_missing").Inc()
				continue
			}
			rendered, err := m.renderer.Render(version, renderContext)
			if err != nil {
				notificationTemplateRenderTotal.WithLabelValues(channelName, kind.Kind, notification.Locale, "error").Inc()
				return fmt.Errorf("notify: render %s %s: %w", channelName, kind.Kind, err)
			}
			notificationTemplateRenderTotal.WithLabelValues(channelName, kind.Kind, notification.Locale, "success").Inc()
			if channelName == model.ChannelEmail {
				deliveries = append(deliveries, model.Delivery{
					ID: generalutil.NewV7(), NotificationID: &notification.ID, UserID: notification.UserID,
					Channel: channelName, EndpointIdentity: "email", Status: model.DeliveryPendingRecipient,
					TemplateVersionID: version.ID, Locale: notification.Locale, RenderedSubject: rendered.Subject,
					RenderedTitle: rendered.Title, RenderedText: rendered.Text, RenderedHTML: rendered.HTML,
					ProviderPayload: rendered.Payload, ContentHash: rendered.Hash, NextAttemptAt: &due, CreatedAt: time.Now().UTC(),
				})
				continue
			}
			devices, err := m.platform.ListActiveDevices(ctx, recipient.userID)
			if err != nil {
				return fmt.Errorf("notify: list active devices: %w", err)
			}
			for _, device := range devices {
				endpointID := device.ID
				deliveries = append(deliveries, model.Delivery{
					ID: generalutil.NewV7(), NotificationID: &notification.ID, UserID: notification.UserID,
					Channel: channelName, EndpointID: &endpointID, EndpointIdentity: device.ID.String(), Status: model.DeliveryScheduled,
					TemplateVersionID: version.ID, Locale: notification.Locale, RenderedTitle: rendered.Title,
					RenderedText: rendered.Text, ProviderPayload: rendered.Payload, ContentHash: rendered.Hash,
					NextAttemptAt: &due, CreatedAt: time.Now().UTC(),
				})
			}
		}
		inbox := model.EventInbox{ID: generalutil.NewV7(), SourceService: "ledger", EventID: eventID,
			EventType: events.TypeTransactionPosted, SchemaVersion: ev.SchemaVersion, PayloadHash: sha256Bytes(d.Body),
			Status: "received", ReceivedAt: time.Now().UTC()}
		inserted, err := m.platform.Plan(ctx, model.PlannedEvent{Inbox: inbox, Notification: notification, Deliveries: deliveries, DigestItems: digestItems})
		if err != nil {
			return fmt.Errorf("notify: plan event: %w", err)
		}
		if inserted {
			notificationLogicalCreatedTotal.WithLabelValues(kind.Kind, kind.Category).Inc()
		} else {
			notificationDuplicatesTotal.WithLabelValues("ledger", events.TypeTransactionPosted).Inc()
		}
		for range digestItems {
			notificationDigestItemsTotal.WithLabelValues(kind.Category).Inc()
		}
	}
	return nil
}

func (m *Module) channelAllowsPlanning(ctx context.Context, channelName string) (bool, error) {
	control, err := m.platform.GetChannelControl(ctx, channelName)
	if err != nil {
		return false, fmt.Errorf("notify: load %s channel control: %w", channelName, err)
	}
	observeNotificationChannelControl(control)
	return control.State == "running" || (control.ExpiresAt != nil && !control.ExpiresAt.After(time.Now().UTC())), nil
}

func effectiveMode(kind registry.Kind, channelName string, preferences []model.Preference, cfg Config) string {
	if channelName == model.ChannelInApp {
		return model.ModeImmediate
	}
	if channelName == model.ChannelEmail && !cfg.EmailEnabled {
		return model.ModeDisabled
	}
	if channelName == model.ChannelPush && !cfg.PushEnabled {
		return model.ModeDisabled
	}
	mode := kind.DefaultModes[channelName]
	for _, preference := range preferences {
		if preference.Category == kind.Category && preference.Channel == channelName {
			mode = preference.Mode
		}
	}
	return mode
}

func sha256Bytes(value []byte) []byte {
	sum := sha256.Sum256(value)
	return sum[:]
}

func (m *Module) activeTemplate(ctx context.Context, kind, channelName, locale string) (notifytemplate.Version, bool, error) {
	if m.platform != nil && m.db != nil {
		if version, ok, err := m.platform.GetActiveTemplate(ctx, kind, channelName, locale); err != nil {
			return notifytemplate.Version{}, false, err
		} else if ok {
			return version, true, nil
		}
	}
	version, ok := notifytemplate.Builtin(kind, channelName, locale)
	return version, ok, nil
}

type recipient struct {
	userID uuid.UUID
	title  string
	body   string
}

// recipientsFor maps one TransactionPosted event to its notification
// recipient(s) (docs/roadmap/archive/25 Task T4 step 3): money_in/withdraw_settle/
// withdraw_cancel notify the single UserID; transfer_p2p notifies BOTH
// parties with distinct sender/receiver copies ("sent"/"received"). An
// event with the relevant *UserID field unset produces zero recipients for
// that side — nothing to notify, not an error (defensive: every current
// processor for these four types always sets UserID, docs/roadmap/archive/25 T4's own
// enrichment step, but this must not panic if a future processor forgets).
func recipientsFor(ev events.TransactionPosted) []recipient {
	var out []recipient
	switch ev.TransactionType {
	case "transfer_p2p":
		if ev.UserID != nil {
			out = append(out, recipient{
				userID: *ev.UserID,
				title:  "Transfer sent",
				body:   fmt.Sprintf("Your %s %s transfer was sent successfully.", ev.Currency, formatMinorAmount(ev.Currency, ev.Amount, ev.MinorUnit)),
			})
		}
		if ev.TargetUserID != nil {
			out = append(out, recipient{
				userID: *ev.TargetUserID,
				title:  "Transfer received",
				body:   fmt.Sprintf("You received a %s %s transfer.", ev.Currency, formatMinorAmount(ev.Currency, ev.Amount, ev.MinorUnit)),
			})
		}
	case "money_in":
		if ev.UserID != nil {
			out = append(out, recipient{
				userID: *ev.UserID,
				title:  "Funds received",
				body:   fmt.Sprintf("Your %s %s top-up was successful and your balance increased.", ev.Currency, formatMinorAmount(ev.Currency, ev.Amount, ev.MinorUnit)),
			})
		}
	case "withdraw_settle":
		if ev.UserID != nil {
			out = append(out, recipient{
				userID: *ev.UserID,
				title:  "Withdrawal successful",
				body:   fmt.Sprintf("Your %s %s withdrawal was processed successfully.", ev.Currency, formatMinorAmount(ev.Currency, ev.Amount, ev.MinorUnit)),
			})
		}
	case "withdraw_cancel":
		if ev.UserID != nil {
			out = append(out, recipient{
				userID: *ev.UserID,
				title:  "Withdrawal canceled",
				body:   fmt.Sprintf("Your %s %s withdrawal was canceled and the funds were returned.", ev.Currency, formatMinorAmount(ev.Currency, ev.Amount, ev.MinorUnit)),
			})
		}
	}
	return out
}

func formatMinorAmount(code, raw string, declaredMinorUnit int16) string {
	minorUnit := declaredMinorUnit
	if declaredMinorUnit > 0 {
		minorUnit = declaredMinorUnit
	} else if registeredMinorUnit, ok := currencyreg.MinorUnit(code); ok {
		minorUnit = registeredMinorUnit
	} else if minorUnit == 0 && code == "USD" {
		// Historical USD events predate the optional MinorUnit field. C4's
		// canonical USD minor unit is two even when the process registry has
		// not yet been loaded.
		minorUnit = 2
	}
	if minorUnit < 0 {
		return raw
	}
	value, err := decimal.NewFromString(raw)
	if err != nil {
		return raw
	}
	return value.Shift(-int32(minorUnit)).StringFixed(int32(minorUnit))
}

// ListNotifications returns userID's own notifications, newest first,
// keyset-paginated on created_at. before.IsZero() starts from the most
// recent. limit<=0 defaults to 50, capped at 200.
func (m *Module) ListNotifications(ctx context.Context, userID uuid.UUID, limit int, before time.Time) ([]Notification, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if m.platform != nil && m.db != nil {
		return m.platform.ListNotifications(ctx, userID, limit, before, false, "", "")
	}
	return m.repo.List(ctx, userID, limit, before)
}

// ListNotificationsFiltered is the additive query surface used by the C3
// HTTP API. Legacy callers continue to use ListNotifications unchanged.
func (m *Module) ListNotificationsFiltered(ctx context.Context, userID uuid.UUID, limit int, before time.Time, unread bool, category, kind string) ([]Notification, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if m.platform != nil && m.db != nil {
		return m.platform.ListNotifications(ctx, userID, limit, before, unread, category, kind)
	}
	return m.repo.List(ctx, userID, limit, before)
}

// MarkRead marks id as read for userID (ownership enforced at the SQL
// layer). Returns ErrNotificationNotFound if no such row exists for that
// (id, userID) pair.
func (m *Module) MarkRead(ctx context.Context, id, userID uuid.UUID) error {
	var matched bool
	var err error
	if m.platform != nil && m.db != nil {
		matched, err = m.platform.MarkRead(ctx, id, userID)
	} else {
		matched, err = m.repo.MarkRead(ctx, id, userID)
	}
	if err != nil {
		return err
	}
	if !matched {
		return ErrNotificationNotFound
	}
	return nil
}
