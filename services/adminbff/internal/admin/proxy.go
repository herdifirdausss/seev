package adminbff

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/platform/transport/http/response"
	"github.com/herdifirdausss/seev/services/adminbff/internal/client"
)

// rewriteProxyPath maps an incoming public-facing request path to the
// downstream service path — the sole piece of arithmetic behind every
// proxy() route registration below, and the exact place a mismatched
// publicPrefix/downstreamPrefix pair silently 404s (a load-testing
// session's routing-bug finding: "/api/v1/admin/ledger/" used to pass its
// own path straight through unchanged instead of rewriting to
// "/api/v1/ledger/admin/", the one downstream mount that actually strips
// cleanly to ledger-service's own "/admin/*" route table). Extracted so the
// rewrite itself is unit-testable without a live downstream.
func rewriteProxyPath(requestPath, rawQuery, publicPrefix, downstreamPrefix string) string {
	suffix := strings.TrimPrefix(requestPath, publicPrefix)
	path := downstreamPrefix + suffix
	if rawQuery != "" {
		path += "?" + rawQuery
	}
	return path
}

func (m *Module) proxy(target string, downstream *client.ServiceClient, publicPrefix, downstreamPrefix string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body []byte
		var err error
		contentType := r.Header.Get("Content-Type")
		if strings.HasPrefix(contentType, "application/x-www-form-urlencoded") {
			// ParseForm is idempotent: if RequireCSRF already called it and
			// drained r.Body, r.PostForm is already populated; calling again is
			// a no-op that reads the cached map rather than re-reading the now-
			// empty body stream.
			if parseErr := r.ParseForm(); parseErr != nil {
				response.BadRequest(w, "invalid form body")
				return
			}
			payload := make(map[string]any, len(r.PostForm))
			for key, items := range r.PostForm {
				if key == "csrf_token" {
					continue // never forward the CSRF token downstream
				}
				if len(items) == 1 {
					payload[key] = items[0]
				} else {
					payload[key] = items
				}
			}
			body, err = json.Marshal(payload)
			if err != nil {
				response.BadRequest(w, "invalid form body")
				return
			}
			contentType = "application/json"
		} else {
			body, err = io.ReadAll(io.LimitReader(r.Body, 4<<20))
			if err != nil {
				response.BadRequest(w, "invalid request body")
				return
			}
		}
		token, err := m.MintDownstreamToken(r.Context())
		if err != nil {
			response.Unauthorized(w, "authentication required")
			return
		}
		path := rewriteProxyPath(r.URL.Path, r.URL.RawQuery, publicPrefix, downstreamPrefix)
		status, headers, responseBody, callErr := downstream.DoRaw(r.Context(), token, r.Method, path, body, contentType)
		if ct := headers.Get("Content-Type"); ct != "" {
			w.Header().Set("Content-Type", ct)
		} else {
			w.Header().Set("Content-Type", "application/json")
		}
		if callErr != nil && status == 0 {
			m.AuditMutation(r.Context(), r, target, http.StatusServiceUnavailable, map[string]any{"error": "unavailable"})
			writeJSONError(w, http.StatusServiceUnavailable, "DOWNSTREAM_UNAVAILABLE", "admin service temporarily unavailable")
			return
		}
		if status == 0 {
			status = http.StatusBadGateway
		}
		m.AuditMutation(r.Context(), r, target, status, map[string]any{"downstream_status": status})
		w.WriteHeader(status)
		_, _ = w.Write(responseBody)
	})
}

func (m *Module) reconUploadProxy() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost && r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 10<<20))
		if err != nil {
			response.BadRequest(w, "invalid upload")
			return
		}
		token, err := m.MintDownstreamToken(r.Context())
		if err != nil {
			response.Unauthorized(w, "authentication required")
			return
		}
		path := "/api/v1/ledger/admin/recon/batches"
		if r.URL.RawQuery != "" {
			path += "?" + r.URL.RawQuery
		}
		status, headers, responseBody, callErr := m.clients.Ledger.DoRaw(r.Context(), token, http.MethodPost, path, body, r.Header.Get("Content-Type"))
		if callErr != nil && status == 0 {
			m.AuditMutation(r.Context(), r, "ledger", http.StatusServiceUnavailable, map[string]any{"error": "unavailable"})
			writeJSONError(w, http.StatusServiceUnavailable, "DOWNSTREAM_UNAVAILABLE", "admin service temporarily unavailable")
			return
		}
		if status == 0 {
			status = http.StatusBadGateway
		}
		if ct := headers.Get("Content-Type"); ct != "" {
			w.Header().Set("Content-Type", ct)
		} else {
			w.Header().Set("Content-Type", "application/json")
		}
		m.AuditMutation(r.Context(), r, "ledger", status, map[string]any{"downstream_status": status, "operation": "recon_import"})
		w.WriteHeader(status)
		_, _ = w.Write(responseBody)
	})
}

func (m *Module) adjustmentDecisionProxy(action string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			response.BadRequest(w, "invalid form")
			return
		}
		id, err := uuid.Parse(r.FormValue("adjustment_id"))
		if err != nil {
			response.BadRequest(w, "invalid adjustment id")
			return
		}
		token, err := m.MintDownstreamToken(r.Context())
		if err != nil {
			response.Unauthorized(w, "authentication required")
			return
		}
		path := "/api/v1/ledger/admin/adjustments/" + id.String() + "/" + action
		status, headers, body, callErr := m.clients.Ledger.DoRaw(r.Context(), token, http.MethodPost, path, []byte("{}"), "application/json")
		if callErr != nil && status == 0 {
			m.AuditMutation(r.Context(), r, "ledger", http.StatusServiceUnavailable, map[string]any{"error": "unavailable", "operation": action})
			writeJSONError(w, http.StatusServiceUnavailable, "DOWNSTREAM_UNAVAILABLE", "admin service temporarily unavailable")
			return
		}
		if status == 0 {
			status = http.StatusBadGateway
		}
		if ct := headers.Get("Content-Type"); ct != "" {
			w.Header().Set("Content-Type", ct)
		} else {
			w.Header().Set("Content-Type", "application/json")
		}
		m.AuditMutation(r.Context(), r, "ledger", status, map[string]any{"downstream_status": status, "operation": action})
		w.WriteHeader(status)
		_, _ = w.Write(body)
	})
}

func (m *Module) fxRateDecisionProxy(action string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			response.BadRequest(w, "invalid form")
			return
		}
		rateID, err := uuid.Parse(r.FormValue("id"))
		if err != nil {
			response.BadRequest(w, "invalid rate id")
			return
		}
		token, err := m.MintDownstreamToken(r.Context())
		if err != nil {
			response.Unauthorized(w, "authentication required")
			return
		}
		var body []byte
		contentType := "application/json"
		if action == "reject" {
			body, err = json.Marshal(map[string]string{"reason": strings.TrimSpace(r.FormValue("reason"))})
			if err != nil {
				response.BadRequest(w, "invalid rejection reason")
				return
			}
		} else {
			body = []byte("{}")
		}
		path := "/api/v1/ledger/admin/fx/rates/" + rateID.String() + "/" + action
		status, headers, responseBody, callErr := m.clients.Ledger.DoRaw(r.Context(), token, http.MethodPost, path, body, contentType)
		if callErr != nil && status == 0 {
			m.AuditMutation(r.Context(), r, "ledger", http.StatusServiceUnavailable, map[string]any{"error": "unavailable", "operation": "fx_rate_" + action})
			writeJSONError(w, http.StatusServiceUnavailable, "DOWNSTREAM_UNAVAILABLE", "admin service temporarily unavailable")
			return
		}
		if status == 0 {
			status = http.StatusBadGateway
		}
		if ct := headers.Get("Content-Type"); ct != "" {
			w.Header().Set("Content-Type", ct)
		} else {
			w.Header().Set("Content-Type", "application/json")
		}
		m.AuditMutation(r.Context(), r, "ledger", status, map[string]any{"downstream_status": status, "operation": "fx_rate_" + action})
		w.WriteHeader(status)
		_, _ = w.Write(responseBody)
	})
}

// notificationTemplateDraftRequest mirrors services/gateway's
// notifytemplate.Version field-for-field with no json tags on either side,
// so plain Go field-name matching (not snake_case) is what the downstream
// decoder expects.
type notificationTemplateDraftRequest struct {
	Kind             string `json:"Kind"`
	Channel          string `json:"Channel"`
	Locale           string `json:"Locale"`
	SubjectTemplate  string `json:"SubjectTemplate"`
	TitleTemplate    string `json:"TitleTemplate"`
	BodyTextTemplate string `json:"BodyTextTemplate"`
	BodyHTMLTemplate string `json:"BodyHTMLTemplate"`
}

// notificationTemplateDraftProxy, notificationTemplateDecisionProxy,
// notificationDeliveryReplayProxy, and notificationChannelControlProxy all
// use r.ParseForm()+r.FormValue like adjustmentDecisionProxy/
// fxRateDecisionProxy above, not a raw r.Body re-read like proxy(): RequireCSRF
// (login.go) already calls r.ParseForm() to read the hidden csrf_token field
// on every plain HTML <form> submission (browsers cannot set the
// X-CSRF-Token header), which permanently drains r.Body. A handler that
// tried its own io.ReadAll(r.Body) afterward — as the generic proxy() does —
// would forward an empty payload downstream.
func (m *Module) notificationTemplateDraftProxy() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			response.BadRequest(w, "invalid form")
			return
		}
		payload := notificationTemplateDraftRequest{
			Kind:             strings.TrimSpace(r.FormValue("kind")),
			Channel:          strings.TrimSpace(r.FormValue("channel")),
			Locale:           strings.TrimSpace(r.FormValue("locale")),
			SubjectTemplate:  r.FormValue("subject_template"),
			TitleTemplate:    r.FormValue("title_template"),
			BodyTextTemplate: r.FormValue("body_text_template"),
			BodyHTMLTemplate: r.FormValue("body_html_template"),
		}
		body, err := json.Marshal(payload)
		if err != nil {
			response.BadRequest(w, "invalid template draft")
			return
		}
		token, err := m.MintDownstreamToken(r.Context())
		if err != nil {
			response.Unauthorized(w, "authentication required")
			return
		}
		path := "/api/v1/admin/gateway/notifications/templates/draft"
		status, headers, responseBody, callErr := m.clients.Gateway.DoRaw(r.Context(), token, http.MethodPost, path, body, "application/json")
		if callErr != nil && status == 0 {
			m.AuditMutation(r.Context(), r, "gateway", http.StatusServiceUnavailable, map[string]any{"error": "unavailable", "operation": "notification_template_draft"})
			writeJSONError(w, http.StatusServiceUnavailable, "DOWNSTREAM_UNAVAILABLE", "admin service temporarily unavailable")
			return
		}
		if status == 0 {
			status = http.StatusBadGateway
		}
		if ct := headers.Get("Content-Type"); ct != "" {
			w.Header().Set("Content-Type", ct)
		} else {
			w.Header().Set("Content-Type", "application/json")
		}
		m.AuditMutation(r.Context(), r, "gateway", status, map[string]any{"downstream_status": status, "operation": "notification_template_draft", "kind": payload.Kind, "channel": payload.Channel})
		w.WriteHeader(status)
		_, _ = w.Write(responseBody)
	})
}

func (m *Module) notificationTemplateDecisionProxy(action string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			response.BadRequest(w, "invalid form")
			return
		}
		id, err := uuid.Parse(r.FormValue("id"))
		if err != nil {
			response.BadRequest(w, "invalid template id")
			return
		}
		token, err := m.MintDownstreamToken(r.Context())
		if err != nil {
			response.Unauthorized(w, "authentication required")
			return
		}
		body := []byte("{}")
		if action == "reject" {
			body, err = json.Marshal(map[string]string{"reason": strings.TrimSpace(r.FormValue("reason"))})
			if err != nil {
				response.BadRequest(w, "invalid rejection reason")
				return
			}
		}
		path := "/api/v1/admin/gateway/notifications/templates/" + id.String() + "/" + action
		status, headers, responseBody, callErr := m.clients.Gateway.DoRaw(r.Context(), token, http.MethodPost, path, body, "application/json")
		if callErr != nil && status == 0 {
			m.AuditMutation(r.Context(), r, "gateway", http.StatusServiceUnavailable, map[string]any{"error": "unavailable", "operation": "notification_template_" + action})
			writeJSONError(w, http.StatusServiceUnavailable, "DOWNSTREAM_UNAVAILABLE", "admin service temporarily unavailable")
			return
		}
		if status == 0 {
			status = http.StatusBadGateway
		}
		if ct := headers.Get("Content-Type"); ct != "" {
			w.Header().Set("Content-Type", ct)
		} else {
			w.Header().Set("Content-Type", "application/json")
		}
		m.AuditMutation(r.Context(), r, "gateway", status, map[string]any{"downstream_status": status, "operation": "notification_template_" + action})
		w.WriteHeader(status)
		_, _ = w.Write(responseBody)
	})
}

func (m *Module) notificationDeliveryReplayProxy() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			response.BadRequest(w, "invalid form")
			return
		}
		id, err := uuid.Parse(r.FormValue("id"))
		if err != nil {
			response.BadRequest(w, "invalid delivery id")
			return
		}
		reason := strings.TrimSpace(r.FormValue("reason"))
		if reason == "" {
			response.BadRequest(w, "replay reason is required")
			return
		}
		token, err := m.MintDownstreamToken(r.Context())
		if err != nil {
			response.Unauthorized(w, "authentication required")
			return
		}
		body, err := json.Marshal(map[string]string{"reason": reason})
		if err != nil {
			response.BadRequest(w, "invalid reason")
			return
		}
		path := "/api/v1/admin/gateway/notifications/deliveries/" + id.String() + "/replay"
		status, headers, responseBody, callErr := m.clients.Gateway.DoRaw(r.Context(), token, http.MethodPost, path, body, "application/json")
		if callErr != nil && status == 0 {
			m.AuditMutation(r.Context(), r, "gateway", http.StatusServiceUnavailable, map[string]any{"error": "unavailable", "operation": "notification_delivery_replay"})
			writeJSONError(w, http.StatusServiceUnavailable, "DOWNSTREAM_UNAVAILABLE", "admin service temporarily unavailable")
			return
		}
		if status == 0 {
			status = http.StatusBadGateway
		}
		if ct := headers.Get("Content-Type"); ct != "" {
			w.Header().Set("Content-Type", ct)
		} else {
			w.Header().Set("Content-Type", "application/json")
		}
		m.AuditMutation(r.Context(), r, "gateway", status, map[string]any{"downstream_status": status, "operation": "notification_delivery_replay"})
		w.WriteHeader(status)
		_, _ = w.Write(responseBody)
	})
}

// notificationChannelControlProxy binds one handler per target state
// (paused/running/drain_only) so each operator button posts to a fixed,
// self-describing URL instead of the browser having to submit an arbitrary
// state value.
func (m *Module) notificationChannelControlProxy(state string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			response.BadRequest(w, "invalid form")
			return
		}
		channel := strings.TrimSpace(r.FormValue("channel"))
		if channel != "email" && channel != "push" && channel != "digest" {
			response.BadRequest(w, "invalid channel")
			return
		}
		reason := strings.TrimSpace(r.FormValue("reason"))
		token, err := m.MintDownstreamToken(r.Context())
		if err != nil {
			response.Unauthorized(w, "authentication required")
			return
		}
		body, err := json.Marshal(map[string]string{"state": state, "reason": reason})
		if err != nil {
			response.BadRequest(w, "invalid channel control request")
			return
		}
		path := "/api/v1/admin/gateway/notifications/channels/" + channel
		status, headers, responseBody, callErr := m.clients.Gateway.DoRaw(r.Context(), token, http.MethodPut, path, body, "application/json")
		if callErr != nil && status == 0 {
			m.AuditMutation(r.Context(), r, "gateway", http.StatusServiceUnavailable, map[string]any{"error": "unavailable", "operation": "notification_channel_" + state})
			writeJSONError(w, http.StatusServiceUnavailable, "DOWNSTREAM_UNAVAILABLE", "admin service temporarily unavailable")
			return
		}
		if status == 0 {
			status = http.StatusBadGateway
		}
		if ct := headers.Get("Content-Type"); ct != "" {
			w.Header().Set("Content-Type", ct)
		} else {
			w.Header().Set("Content-Type", "application/json")
		}
		m.AuditMutation(r.Context(), r, "gateway", status, map[string]any{"downstream_status": status, "operation": "notification_channel_" + state, "channel": channel})
		w.WriteHeader(status)
		_, _ = w.Write(responseBody)
	})
}

// notificationDeliveryDetailProxy takes the delivery ID from a query
// parameter rather than the URL path: a plain HTML <form method="get"> can
// only append its fields as a query string, it cannot interpolate a value
// into the middle of the target path the way a JS client could.
func (m *Module) notificationDeliveryDetailProxy() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := uuid.Parse(r.URL.Query().Get("id"))
		if err != nil {
			response.BadRequest(w, "invalid delivery id")
			return
		}
		token, err := m.MintDownstreamToken(r.Context())
		if err != nil {
			response.Unauthorized(w, "authentication required")
			return
		}
		path := "/api/v1/admin/gateway/notifications/deliveries/" + id.String()
		status, headers, body, callErr := m.clients.Gateway.DoRaw(r.Context(), token, http.MethodGet, path, nil, "")
		if callErr != nil && status == 0 {
			writeJSONError(w, http.StatusServiceUnavailable, "DOWNSTREAM_UNAVAILABLE", "admin service temporarily unavailable")
			return
		}
		if status == 0 {
			status = http.StatusBadGateway
		}
		if ct := headers.Get("Content-Type"); ct != "" {
			w.Header().Set("Content-Type", ct)
		} else {
			w.Header().Set("Content-Type", "application/json")
		}
		w.WriteHeader(status)
		_, _ = w.Write(body)
	})
}

func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	response.ErrorStatus(w, status, code, message)
}
