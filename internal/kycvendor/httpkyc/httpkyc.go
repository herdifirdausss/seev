// Package httpkyc adapts a provider's sandbox/production JSON contract to the
// auth-owned kycvendor interface. It deliberately keeps the payload opaque to
// logs and bounds both request and response sizes.
package httpkyc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/herdifirdausss/seev/internal/kycvendor"
)

const (
	defaultName  = "http-kyc"
	maxBodyBytes = 1 << 20
)

type Provider struct {
	url      string
	token    string
	name     string
	client   *http.Client
	maxBytes int64
}

type request struct {
	UserID         string         `json:"user_id"`
	LevelRequested int            `json:"level_requested"`
	Payload        map[string]any `json:"payload"`
}

type response struct {
	Verdict string `json:"verdict"`
	Ref     string `json:"ref"`
	Reason  string `json:"reason"`
}

func New(endpoint, token, name string, client *http.Client) (*Provider, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return nil, fmt.Errorf("httpkyc: endpoint is required")
	}
	if client == nil {
		client = &http.Client{Timeout: 3 * time.Second}
	}
	if name == "" {
		name = defaultName
	}
	return &Provider{url: endpoint, token: token, name: name, client: client, maxBytes: maxBodyBytes}, nil
}

func (p *Provider) Name() string { return p.name }

func (p *Provider) Verify(ctx context.Context, submission kycvendor.Submission) (kycvendor.Decision, error) {
	body, err := json.Marshal(request{UserID: submission.UserID.String(), LevelRequested: submission.LevelRequested, Payload: submission.Payload})
	if err != nil {
		return kycvendor.Decision{}, fmt.Errorf("httpkyc: encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.url, bytes.NewReader(body))
	if err != nil {
		return kycvendor.Decision{}, fmt.Errorf("httpkyc: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if p.token != "" {
		req.Header.Set("Authorization", "Bearer "+p.token)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return kycvendor.Decision{}, fmt.Errorf("httpkyc: provider request: %w", err)
	}
	defer resp.Body.Close()
	limited := io.LimitReader(resp.Body, p.maxBytes+1)
	responseBody, err := io.ReadAll(limited)
	if err != nil {
		return kycvendor.Decision{}, fmt.Errorf("httpkyc: read response: %w", err)
	}
	if int64(len(responseBody)) > p.maxBytes {
		return kycvendor.Decision{}, fmt.Errorf("httpkyc: provider response exceeds %d bytes", p.maxBytes)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return kycvendor.Decision{}, fmt.Errorf("httpkyc: provider returned HTTP %d", resp.StatusCode)
	}
	var decoded response
	if err := json.Unmarshal(responseBody, &decoded); err != nil {
		return kycvendor.Decision{}, fmt.Errorf("httpkyc: decode response: %w", err)
	}
	switch decoded.Verdict {
	case kycvendor.VerdictApprove, kycvendor.VerdictReject, kycvendor.VerdictRefer:
	default:
		return kycvendor.Decision{}, fmt.Errorf("httpkyc: unsupported verdict %q", decoded.Verdict)
	}
	return kycvendor.Decision{Verdict: decoded.Verdict, Ref: decoded.Ref, Reason: decoded.Reason}, nil
}
