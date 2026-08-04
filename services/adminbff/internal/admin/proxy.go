package adminbff

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
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
		body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
		if err != nil {
			response.BadRequest(w, "invalid request body")
			return
		}
		contentType := r.Header.Get("Content-Type")
		if strings.HasPrefix(contentType, "application/x-www-form-urlencoded") {
			values, parseErr := url.ParseQuery(string(body))
			if parseErr != nil {
				response.BadRequest(w, "invalid form body")
				return
			}
			payload := make(map[string]any, len(values))
			for key, items := range values {
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

func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	response.ErrorStatus(w, status, code, message)
}
