package handler

import (
	"errors"
	"io"
	"log/slog"
	"net/http"

	payinv1 "github.com/herdifirdausss/seev/gen/payin/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// maxWebhookBodyBytes caps a single webhook delivery — vendor payment
// webhooks are small JSON payloads; this bounds worst-case memory per
// request on a small box (docs/roadmap/archive/22 Task T3).
const maxWebhookBodyBytes = 64 << 10 // 64KiB

// webhookHandler serves POST /webhooks/{vendor} (docs/roadmap/archive/22 Task T3,
// decision K-T1) — deliberately mounted OUTSIDE the JWT/CORS/RequireJSON
// chain: a payment vendor authenticates itself via a per-vendor signature
// (internal/vendorgw), verified inside deps.Payin.HandleWebhook, never via
// this app's own end-user auth. Response body is intentionally minimal —
// never leak internal error detail to a vendor.
func webhookHandler(deps *Dependencies, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Payin == nil {
			// No vendor configured at all (default) — byte-identical to
			// before this feature existed (docs/roadmap/archive/22 Task T3 DoD).
			http.NotFound(w, r)
			return
		}
		vendor := r.PathValue("vendor")

		r.Body = http.MaxBytesReader(w, r.Body, maxWebhookBodyBytes)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				w.WriteHeader(http.StatusRequestEntityTooLarge)
				return
			}
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		headers := make(map[string]string, len(r.Header))
		for key := range r.Header {
			headers[key] = r.Header.Get(key)
		}
		result, err := deps.Payin.HandleWebhook(r.Context(), &payinv1.HandleWebhookRequest{Vendor: vendor, Headers: headers, RawBody: body})
		switch {
		case err == nil && result.GetResult() == payinv1.WebhookResult_WEBHOOK_RESULT_BUSINESS_FAILURE:
			logger.Error("payin webhook: business failure", slog.String("vendor", vendor))
			writeWebhookAck(w, http.StatusOK)
		case err == nil:
			writeWebhookAck(w, http.StatusOK)
		case status.Code(err) == codes.NotFound:
			http.NotFound(w, r)
		case status.Code(err) == codes.Unauthenticated:
			w.WriteHeader(http.StatusUnauthorized)
		case status.Code(err) == codes.Unavailable && status.Convert(err).Message() == "screening dependency unavailable":
			// docs/roadmap/archive/45 Task T3/K4 — fraud-service is reachable but its
			// velocity dependency is down. Same 503-so-the-vendor-retries
			// response as the generic infra case below (this handler's own
			// minimal-body convention deliberately doesn't distinguish
			// error causes to an external vendor), but logged at WARN, not
			// ERROR — this is an expected, self-healing degraded state,
			// not a bug.
			logger.Warn("payin webhook: screening dependency unavailable, failing closed", slog.String("vendor", vendor))
			w.WriteHeader(http.StatusServiceUnavailable)
		default:
			// Infra failure — the event is still 'received'; the vendor's
			// own retry machinery is the queue (docs/roadmap/archive/22 Task T2 step
			// 2), so it should redeliver.
			logger.Error("payin webhook: infra failure", slog.String("vendor", vendor), slog.Any("error", err))
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}
}

func writeWebhookAck(w http.ResponseWriter, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"received":true}`))
}
