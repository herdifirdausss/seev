package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/platform/security/middleware"
	"github.com/herdifirdausss/seev/internal/platform/transport/http/response"
	"github.com/herdifirdausss/seev/services/gateway/internal/notification/model"
	"github.com/herdifirdausss/seev/services/gateway/internal/notification/registry"
	"github.com/herdifirdausss/seev/services/gateway/internal/notification/repository"
	notifytemplate "github.com/herdifirdausss/seev/services/gateway/internal/notification/template"
)

// AdminRouter is mounted only on Gateway's internal listener and then
// protected by the Gateway role middleware. It exposes operational inspection
// and replay, never arbitrary recipient sends.
func (m *Module) AdminRouter() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /notifications/templates", m.adminTemplates)
	mux.HandleFunc("POST /notifications/templates/draft", m.adminCreateTemplateDraft)
	mux.HandleFunc("POST /notifications/templates/{id}/submit", m.adminSubmitTemplate)
	mux.HandleFunc("POST /notifications/templates/{id}/approve", m.adminApproveTemplate)
	mux.HandleFunc("POST /notifications/templates/{id}/reject", m.adminRejectTemplate)
	mux.HandleFunc("POST /notifications/templates/{id}/retire", m.adminRetireTemplate)
	mux.HandleFunc("GET /notifications/deliveries", m.adminDeliveries)
	mux.HandleFunc("GET /notifications/deliveries/{id}", m.adminDelivery)
	mux.HandleFunc("POST /notifications/deliveries/{id}/replay", m.adminReplay)
	mux.HandleFunc("GET /notifications/channels/{channel}", m.adminChannel)
	mux.HandleFunc("PUT /notifications/channels/{channel}", m.adminSetChannel)
	return mux
}

func (m *Module) adminTemplates(w http.ResponseWriter, r *http.Request) {
	type entry struct {
		Kind     registry.Kind                     `json:"kind"`
		Channels map[string]notifytemplate.Version `json:"channels"`
	}
	kinds := registry.All()
	sort.Slice(kinds, func(i, j int) bool { return kinds[i].Kind < kinds[j].Kind })
	out := make([]entry, 0, len(kinds))
	for _, kind := range kinds {
		channels := map[string]notifytemplate.Version{}
		if m.platform != nil && m.db != nil {
			versions, err := m.platform.ListTemplateVersions(r.Context(), kind.Kind, "", "")
			if err != nil {
				response.InternalServerError(w, err)
				return
			}
			for _, version := range versions {
				if _, exists := channels[version.Channel]; !exists || version.Status == notifytemplate.StatusActive {
					channels[version.Channel] = version
				}
			}
		}
		// Built-ins are still shown when the migration has not seeded a
		// channel yet; they are deterministic fallback evidence, not an
		// implicit arbitrary-send path.
		for _, channelName := range []string{model.ChannelInApp, model.ChannelEmail, model.ChannelPush} {
			if _, exists := channels[channelName]; exists {
				continue
			}
			if version, ok := notifytemplate.Builtin(kind.Kind, channelName, notifytemplate.DefaultLocale); ok {
				channels[channelName] = version
			}
		}
		out = append(out, entry{Kind: kind, Channels: channels})
	}
	response.OK(w, map[string]any{"templates": out})
}

func (m *Module) adminCreateTemplateDraft(w http.ResponseWriter, r *http.Request) {
	if !hasRole(r, "admin", "admin_maker") {
		response.Forbidden(w, "maker role required")
		return
	}
	if m.platform == nil || m.db == nil {
		response.ServiceUnavailable(w, "NOTIFICATION_CHANNEL_UNAVAILABLE", "template store is unavailable")
		return
	}
	var v notifytemplate.Version
	if !response.Decode(w, r, &v) {
		return
	}
	kind, known := registry.Lookup(v.Kind)
	if !known || !validChannel(v.Channel) || v.Locale == "" || v.BodyTextTemplate == "" {
		response.BadRequest(w, "invalid template draft")
		return
	}
	if v.Channel == model.ChannelInApp && v.BodyHTMLTemplate != "" {
		response.BadRequest(w, "in-app templates cannot contain HTML")
		return
	}
	if v.Channel == model.ChannelPush && v.BodyHTMLTemplate != "" {
		response.BadRequest(w, "push templates cannot contain HTML")
		return
	}
	if kind.Kind == model.KindDailyDigest && v.Channel != model.ChannelEmail {
		response.BadRequest(w, "daily digest supports email only")
		return
	}
	if err := m.validateTemplateFixture(v); err != nil {
		response.BadRequest(w, "template fixture does not render: "+err.Error())
		return
	}
	v.Status = notifytemplate.StatusDraft
	v.ID = uuid.New()
	claims := middleware.GetClaims(r.Context())
	actor := "operator"
	if claims != nil {
		actor = claims.UserID
	}
	if err := m.platform.CreateTemplateDraft(r.Context(), v, actor); err != nil {
		response.InternalServerError(w, err)
		return
	}
	response.Created(w, map[string]any{"id": v.ID, "status": notifytemplate.StatusDraft})
}

func (m *Module) validateTemplateFixture(version notifytemplate.Version) error {
	version.Status = notifytemplate.StatusActive
	if version.Kind == model.KindDailyDigest {
		_, err := m.renderer.RenderDigest(version, model.DigestRenderContext{
			WindowDate: "2026-07-28",
			Items:      []model.DigestItemContext{{Title: "Transfer sent", Body: "IDR 125,000", DeepLink: "/transactions/00000000-0000-0000-0000-000000000001"}},
			MoreCount:  2,
		})
		return err
	}
	context := model.RenderContext{
		NotificationID: "00000000-0000-0000-0000-000000000001",
		Amount:         model.MoneyContext{Minor: "125000", Currency: "IDR", Display: notifytemplate.FormatMoney(model.MoneyContext{Minor: "125000", Currency: "IDR"})},
		Transaction:    model.TransactionContext{ID: "00000000-0000-0000-0000-000000000002", PostedAt: time.Date(2026, 7, 28, 8, 0, 0, 0, time.UTC)},
		Action:         model.ActionContext{DeepLink: "/transactions/00000000-0000-0000-0000-000000000002"},
	}
	_, err := m.renderer.Render(version, context)
	return err
}
func (m *Module) adminSubmitTemplate(w http.ResponseWriter, r *http.Request) {
	if !hasRole(r, "admin", "admin_maker") {
		response.Forbidden(w, "maker role required")
		return
	}
	m.templateAction(w, r, func(id uuid.UUID, actor string) error { return m.platform.SubmitTemplate(r.Context(), id, actor) })
}
func (m *Module) adminApproveTemplate(w http.ResponseWriter, r *http.Request) {
	if !hasRole(r, "admin", "admin_checker") {
		response.Forbidden(w, "checker role required")
		return
	}
	if m.platform == nil || m.db == nil {
		response.ServiceUnavailable(w, "NOTIFICATION_CHANNEL_UNAVAILABLE", "template store is unavailable")
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		response.BadRequest(w, "invalid template id")
		return
	}
	version, found, err := m.platform.GetTemplateVersion(r.Context(), id)
	if err != nil || !found {
		response.Conflict(w, "template transition was rejected")
		return
	}
	claims := middleware.GetClaims(r.Context())
	actor := "operator"
	if claims != nil {
		actor = claims.UserID
	}
	if err := m.platform.ApproveTemplate(r.Context(), id, actor); err != nil {
		response.Conflict(w, "template transition was rejected")
		return
	}
	notificationTemplatePublishTotal.WithLabelValues(version.Channel, version.Kind, "success").Inc()
	response.NoContent(w)
}
func (m *Module) adminRejectTemplate(w http.ResponseWriter, r *http.Request) {
	if !hasRole(r, "admin", "admin_checker") {
		response.Forbidden(w, "checker role required")
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	if !response.Decode(w, r, &req) {
		return
	}
	m.templateActionWithReason(w, r, req.Reason, func(id uuid.UUID, actor string, reason string) error {
		return m.platform.RejectTemplate(r.Context(), id, actor, reason)
	})
}
func (m *Module) adminRetireTemplate(w http.ResponseWriter, r *http.Request) {
	if !hasRole(r, "admin", "admin_checker") {
		response.Forbidden(w, "checker role required")
		return
	}
	m.templateAction(w, r, func(id uuid.UUID, actor string) error { return m.platform.RetireTemplate(r.Context(), id, actor) })
}
func (m *Module) templateAction(w http.ResponseWriter, r *http.Request, action func(uuid.UUID, string) error) {
	m.templateActionWithReason(w, r, "", func(id uuid.UUID, actor, _ string) error { return action(id, actor) })
}
func (m *Module) templateActionWithReason(w http.ResponseWriter, r *http.Request, reason string, action func(uuid.UUID, string, string) error) {
	if m.platform == nil || m.db == nil {
		response.ServiceUnavailable(w, "NOTIFICATION_CHANNEL_UNAVAILABLE", "template store is unavailable")
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		response.BadRequest(w, "invalid template id")
		return
	}
	claims := middleware.GetClaims(r.Context())
	actor := "operator"
	if claims != nil {
		actor = claims.UserID
	}
	if err := action(id, actor, reason); err != nil {
		response.Conflict(w, "template transition was rejected")
		return
	}
	response.NoContent(w)
}

func (m *Module) adminDeliveries(w http.ResponseWriter, r *http.Request) {
	if m.platform == nil || m.db == nil {
		response.ServiceUnavailable(w, "NOTIFICATION_CHANNEL_UNAVAILABLE", "delivery store is unavailable")
		return
	}
	limit := 100
	list, err := m.platform.ListDeliveries(r.Context(), r.URL.Query().Get("status"), r.URL.Query().Get("channel"), limit)
	if err != nil {
		response.InternalServerError(w, err)
		return
	}
	type safe struct {
		ID           uuid.UUID `json:"id"`
		UserID       uuid.UUID `json:"user_id"`
		Channel      string    `json:"channel"`
		Status       string    `json:"status"`
		AttemptCount int       `json:"attempt_count"`
		ErrorCode    string    `json:"error_code,omitempty"`
		CreatedAt    time.Time `json:"created_at"`
		UpdatedAt    time.Time `json:"updated_at"`
	}
	out := make([]safe, 0, len(list))
	for _, d := range list {
		out = append(out, safe{ID: d.ID, UserID: d.UserID, Channel: d.Channel, Status: d.Status, AttemptCount: d.AttemptCount, ErrorCode: d.LastErrorCode, CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt})
	}
	response.OK(w, map[string]any{"deliveries": out})
}

func (m *Module) adminDelivery(w http.ResponseWriter, r *http.Request) {
	if m.platform == nil || m.db == nil {
		response.ServiceUnavailable(w, "NOTIFICATION_CHANNEL_UNAVAILABLE", "delivery store is unavailable")
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		response.NotFound(w, "delivery not found")
		return
	}
	d, err := m.platform.GetDelivery(r.Context(), id)
	if errors.Is(err, repository.ErrDeliveryNotFound) {
		response.NotFound(w, "delivery not found")
		return
	}
	if err != nil {
		response.InternalServerError(w, err)
		return
	}
	response.OK(w, map[string]any{"id": d.ID, "notification_id": d.NotificationID, "channel": d.Channel, "status": d.Status, "attempt_count": d.AttemptCount, "endpoint_id": d.EndpointID, "recipient_fingerprint": fingerprintSuffix(d.RecipientFingerprint), "template_version_id": d.TemplateVersionID, "locale": d.Locale, "last_error_code": d.LastErrorCode, "created_at": d.CreatedAt, "updated_at": d.UpdatedAt})
}

func (m *Module) adminReplay(w http.ResponseWriter, r *http.Request) {
	if !isOperatorMutation(r) {
		response.Forbidden(w, "checker or maker role required")
		return
	}
	if m.platform == nil || m.db == nil {
		response.ServiceUnavailable(w, "NOTIFICATION_CHANNEL_UNAVAILABLE", "delivery store is unavailable")
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		response.NotFound(w, "delivery not found")
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	if !response.Decode(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Reason) == "" || len(req.Reason) > 500 {
		response.BadRequest(w, "replay reason is required")
		return
	}
	delivery, err := m.platform.GetDelivery(r.Context(), id)
	if errors.Is(err, repository.ErrDeliveryNotFound) {
		response.NotFound(w, "delivery not found")
		return
	}
	if err != nil {
		response.InternalServerError(w, err)
		return
	}
	if delivery.Status != model.DeliveryDead && delivery.Status != model.DeliveryBlocked {
		response.Conflict(w, "only dead or blocked deliveries can be replayed")
		return
	}
	if delivery.Status == model.DeliveryBlocked {
		if err := m.rebuildBlockedDelivery(r.Context(), delivery); err != nil {
			response.Conflict(w, "blocked delivery cannot be replayed until its template is valid")
			return
		}
	}
	if err := m.platform.ReplayDelivery(r.Context(), id, time.Now()); err != nil {
		response.InternalServerError(w, err)
		return
	}
	response.NoContent(w)
}

func (m *Module) rebuildBlockedDelivery(ctx context.Context, delivery model.Delivery) error {
	if delivery.NotificationID == nil {
		return errors.New("blocked delivery has no notification")
	}
	notification, err := m.platform.GetNotification(ctx, delivery.UserID, *delivery.NotificationID)
	if err != nil {
		return err
	}
	if _, ok := registry.Lookup(notification.Kind); !ok {
		return fmt.Errorf("unknown notification kind %q", notification.Kind)
	}
	version, ok, err := m.activeTemplate(ctx, notification.Kind, delivery.Channel, notification.Locale)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("active template is missing")
	}
	var renderContext model.RenderContext
	if err := json.Unmarshal(notification.Context, &renderContext); err != nil {
		return fmt.Errorf("decode notification render context: %w", err)
	}
	rendered, err := m.renderer.Render(version, renderContext)
	if err != nil {
		return err
	}
	if err := m.platform.UpdateDeliverySnapshot(ctx, delivery.ID, version, rendered); err != nil {
		return err
	}
	notificationTemplateRenderTotal.WithLabelValues(delivery.Channel, notification.Kind, notification.Locale, "success").Inc()
	return nil
}

func (m *Module) adminChannel(w http.ResponseWriter, r *http.Request) {
	if m.platform == nil || m.db == nil {
		response.ServiceUnavailable(w, "NOTIFICATION_CHANNEL_UNAVAILABLE", "channel control is unavailable")
		return
	}
	channelName := r.PathValue("channel")
	if channelName != "email" && channelName != "push" && channelName != "digest" {
		response.NotFound(w, "channel not found")
		return
	}
	c, err := m.platform.GetChannelControl(r.Context(), channelName)
	if err != nil {
		response.InternalServerError(w, err)
		return
	}
	response.OK(w, c)
}
func (m *Module) adminSetChannel(w http.ResponseWriter, r *http.Request) {
	if !isOperatorMutation(r) {
		response.Forbidden(w, "maker or checker role required")
		return
	}
	channelName := r.PathValue("channel")
	if channelName != "email" && channelName != "push" && channelName != "digest" {
		response.NotFound(w, "channel not found")
		return
	}
	var req struct {
		State  string `json:"state"`
		Reason string `json:"reason"`
	}
	if !response.Decode(w, r, &req) {
		return
	}
	if req.State != "running" && req.State != "paused" && req.State != "drain_only" {
		response.BadRequest(w, "invalid channel state")
		return
	}
	claims := middleware.GetClaims(r.Context())
	actor := "operator"
	if claims != nil {
		actor = claims.UserID
	}
	if err := m.platform.SetChannelControl(r.Context(), model.ChannelControl{Channel: channelName, State: req.State, Reason: req.Reason, ChangedBy: actor}); err != nil {
		response.InternalServerError(w, err)
		return
	}
	response.NoContent(w)
}
func hasRole(r *http.Request, roles ...string) bool {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		return false
	}
	return slices.Contains(roles, claims.Role)
}
func isOperatorMutation(r *http.Request) bool {
	return hasRole(r, "admin", "admin_maker", "admin_checker")
}
func fingerprintSuffix(value []byte) string {
	if len(value) == 0 {
		return ""
	}
	start := max(len(value)-4, 0)
	return fmt.Sprintf("%x", value[start:])
}
