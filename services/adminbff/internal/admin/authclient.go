package adminbff

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

var ErrInvalidOperator = errors.New("adminbff: invalid operator credentials")

type AuthClient struct {
	baseURL string
	client  *http.Client
}

type AuthUser struct {
	ID    uuid.UUID `json:"id"`
	Email string    `json:"email"`
	Role  string    `json:"role"`
}

type authLoginResponse struct {
	Success bool `json:"success"`
	Data    struct {
		User AuthUser `json:"user"`
	} `json:"data"`
}

func NewAuthClient(baseURL string) *AuthClient {
	return &AuthClient{baseURL: strings.TrimRight(baseURL, "/"), client: &http.Client{Timeout: 5 * time.Second}}
}

func (c *AuthClient) Login(ctx context.Context, email, password string) (AuthUser, error) {
	payload, err := json.Marshal(map[string]string{"email": email, "password": password})
	if err != nil {
		return AuthUser{}, ErrInvalidOperator
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v1/auth/login", bytes.NewReader(payload))
	if err != nil {
		return AuthUser{}, fmt.Errorf("adminbff: build auth login request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return AuthUser{}, fmt.Errorf("adminbff: auth login request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return AuthUser{}, ErrInvalidOperator
	}
	var envelope authLoginResponse
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil || !envelope.Success {
		return AuthUser{}, ErrInvalidOperator
	}
	if envelope.Data.User.ID == uuid.Nil || envelope.Data.User.Email == "" {
		return AuthUser{}, ErrInvalidOperator
	}
	return envelope.Data.User, nil
}
