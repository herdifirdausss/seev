// Package client contains wire-only HTTP clients for existing admin
// surfaces. It intentionally imports no domain module or repository package.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

var ErrUnavailable = errors.New("adminbff: downstream service unavailable")

type DownstreamError struct {
	StatusCode int
	Message    string
}

func (e *DownstreamError) Error() string {
	return fmt.Sprintf("downstream returned %d: %s", e.StatusCode, e.Message)
}

type ServiceClient struct {
	Name    string
	BaseURL string
	HTTP    *http.Client
}

type Clients struct {
	Auth, AuthAdmin, Ledger, Payin, Payout, Fraud, Gateway *ServiceClient
}

func New(name, baseURL string) *ServiceClient {
	return &ServiceClient{Name: name, BaseURL: strings.TrimRight(baseURL, "/"), HTTP: &http.Client{Timeout: 5 * time.Second}}
}

func NewClients(auth, authAdmin, ledger, payin, payout, fraud, gateway string) Clients {
	return Clients{Auth: New("auth", auth), AuthAdmin: New("auth-admin", authAdmin), Ledger: New("ledger", ledger), Payin: New("payin", payin), Payout: New("payout", payout), Fraud: New("fraud", fraud), Gateway: New("gateway", gateway)}
}

func (c *ServiceClient) Do(ctx context.Context, token, method, path string, body []byte) (int, http.Header, []byte, error) {
	return c.DoRaw(ctx, token, method, path, body, "application/json")
}

// DoRaw is the same bounded downstream call as Do, but lets the BFF preserve
// a multipart upload for the ledger reconciliation endpoint.
func (c *ServiceClient) DoRaw(ctx context.Context, token, method, path string, body []byte, contentType string) (int, http.Header, []byte, error) {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, bytes.NewReader(body))
	if err != nil {
		return 0, nil, nil, fmt.Errorf("%s client request: %w", c.Name, err)
	}
	req.Header.Set("Accept", "application/json")
	if len(body) > 0 && contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("%w: %s: %v", ErrUnavailable, c.Name, err)
	}
	defer resp.Body.Close()
	data, readErr := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if readErr != nil {
		return resp.StatusCode, resp.Header, nil, fmt.Errorf("%s client response: %w", c.Name, readErr)
	}
	if resp.StatusCode >= 500 {
		return resp.StatusCode, resp.Header, data, fmt.Errorf("%w: %s returned %d", ErrUnavailable, c.Name, resp.StatusCode)
	}
	if resp.StatusCode >= 400 {
		message := strings.TrimSpace(string(data))
		var envelope struct {
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(data, &envelope) == nil && envelope.Error != nil && envelope.Error.Message != "" {
			message = envelope.Error.Message
		}
		return resp.StatusCode, resp.Header, data, &DownstreamError{StatusCode: resp.StatusCode, Message: message}
	}
	return resp.StatusCode, resp.Header, data, nil
}
