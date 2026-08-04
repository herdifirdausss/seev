package push

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/herdifirdausss/seev/services/gateway/internal/notification/channel"
)

type Sender struct {
	URL     string
	Token   string
	Client  *http.Client
	Timeout time.Duration
}

func NewSender(url, token string, client *http.Client, timeout time.Duration) *Sender {
	if client == nil {
		client = &http.Client{}
	}
	return &Sender{URL: strings.TrimRight(url, "/"), Token: token, Client: client, Timeout: timeout}
}

func (s *Sender) Send(ctx context.Context, message channel.PushMessage) (channel.ProviderResult, error) {
	payload := map[string]any{"delivery_id": message.DeliveryID, "token": message.Token, "platform": message.Platform, "notification": map[string]string{"title": message.Title, "body": message.Body}, "data": message.Data}
	body, err := json.Marshal(payload)
	if err != nil {
		return channel.ProviderResult{Permanent: true, ErrorCode: "push_encode"}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.URL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return channel.ProviderResult{Permanent: true, ErrorCode: "push_request"}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Idempotency-Key", message.DeliveryID)
	if s.Token != "" {
		req.Header.Set("Authorization", "Bearer "+s.Token)
	}
	client := s.Client
	timeout := s.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	callCtx, cancel := context.WithTimeout(req.Context(), timeout)
	defer cancel()
	req = req.WithContext(callCtx)
	resp, err := client.Do(req)
	if err != nil {
		return channel.ProviderResult{ErrorCode: "push_transport"}, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
	excerpt := string(raw)
	if len(excerpt) > 512 {
		excerpt = excerpt[:512]
	}
	result := channel.ProviderResult{StatusCode: resp.StatusCode, ResponseExcerpt: excerpt}
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		result.Accepted = true
		result.ProviderMessageID = resp.Header.Get("X-Provider-Message-ID")
		return result, nil
	case resp.StatusCode == http.StatusGone || resp.StatusCode == http.StatusBadRequest:
		result.Permanent = true
		result.InvalidEndpoint = true
		result.ErrorCode = "push_invalid_token"
	case resp.StatusCode == http.StatusTooManyRequests:
		result.ErrorCode = "push_rate_limited"
	default:
		result.ErrorCode = fmt.Sprintf("push_http_%d", resp.StatusCode)
	}
	return result, fmt.Errorf("push provider returned %s", resp.Status)
}
