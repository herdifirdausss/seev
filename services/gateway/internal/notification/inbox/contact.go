package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

type HTTPContactResolver struct {
	BaseURL string
	Token   string
	Client  *http.Client
}

func (r *HTTPContactResolver) Resolve(ctx context.Context, userID string) (string, bool, bool, error) {
	parsed, err := uuid.Parse(userID)
	if err != nil {
		return "", false, false, fmt.Errorf("invalid contact user id")
	}
	client := r.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	base := strings.TrimRight(r.BaseURL, "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/internal/v1/users/"+url.PathEscape(parsed.String())+"/notification-contact", nil)
	if err != nil {
		return "", false, false, err
	}
	req.Header.Set("Accept", "application/json")
	if r.Token != "" {
		req.Header.Set("Authorization", "Bearer "+r.Token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", false, false, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return "", false, false, err
	}
	if resp.StatusCode == http.StatusNotFound {
		return "", false, false, nil
	}
	if resp.StatusCode >= 500 {
		return "", false, false, fmt.Errorf("auth contact endpoint returned %s", resp.Status)
	}
	if resp.StatusCode >= 400 {
		return "", false, false, fmt.Errorf("auth contact endpoint rejected request")
	}
	var out contact
	if err := json.Unmarshal(body, &out); err != nil {
		return "", false, false, fmt.Errorf("decode auth contact response: %w", err)
	}
	return out.Email, out.EmailVerified, out.UserStatus == "active", nil
}
