// Package alerting provides a minimal, generic outbound alert mechanism —
// currently just a webhook poster. Deliberately not ledger-specific: any
// component that needs to fire an external alert can use AlertFunc
// (docs/plan/12 Task T4).
package alerting

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// AlertFunc sends one alert. Implementations must be fire-and-forget: a
// single attempt, no internal retry — the caller (e.g. the ledger
// verifier) must keep running its own checks regardless of whether an
// individual alert delivery succeeds.
type AlertFunc func(ctx context.Context, severity, message string) error

const defaultTimeout = 5 * time.Second

// webhookPayload is intentionally minimal and generic — compatible with a
// Slack Incoming Webhook only if Slack's endpoint is fronted by something
// that reshapes this JSON (Slack itself expects {"text": ...}), but trivial
// to front with n8n/Zapier/a small Lambda for PagerDuty Events API or any
// other richer format. Keeping this payload simple avoids coupling the
// ledger to one specific alerting vendor's schema.
type webhookPayload struct {
	Severity  string `json:"severity"`
	Message   string `json:"message"`
	Service   string `json:"service"`
	Timestamp string `json:"timestamp"`
}

// NewWebhookAlerter returns an AlertFunc that POSTs a JSON payload to url.
// httpClient may be nil (a client with defaultTimeout is used then) —
// pass one only if the caller wants different transport settings.
//
// Failure to deliver is returned as an error for the caller to log; it is
// NEVER retried internally and never blocks beyond defaultTimeout.
func NewWebhookAlerter(url string, httpClient *http.Client) AlertFunc {
	client := httpClient
	if client == nil {
		client = &http.Client{Timeout: defaultTimeout}
	}

	return func(ctx context.Context, severity, message string) error {
		payload := webhookPayload{
			Severity:  severity,
			Message:   message,
			Service:   "seev-ledger",
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		}
		body, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("alerting: marshal payload: %w", err)
		}

		reqCtx, cancel := context.WithTimeout(ctx, defaultTimeout)
		defer cancel()

		req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("alerting: build request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("alerting: send webhook: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode >= 300 {
			return fmt.Errorf("alerting: webhook returned status %d", resp.StatusCode)
		}
		return nil
	}
}
