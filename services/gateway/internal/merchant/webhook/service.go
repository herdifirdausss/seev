// Package webhook is Plan 57 T7's outbound webhook relay — endpoint
// management, envelope construction, HMAC signing, an SSRF-safe delivery
// client, and the leasing relay worker that consumes ledger events and
// delivers them to merchant-configured endpoints.
package webhook

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/url"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/platform/database/identifiers"
	"github.com/herdifirdausss/seev/internal/platform/security/crypto"
	"github.com/herdifirdausss/seev/services/gateway/internal/merchant/model"
	"github.com/herdifirdausss/seev/services/gateway/internal/merchant/repository"
)

// secretByteLen matches this codebase's other high-entropy secret
// generation (e.g. services/gateway/internal/merchant/auth's key material) — 256 bits.
const secretByteLen = 32

// Sentinel validation errors for CreateEndpoint, wrapped with %w so a
// caller (adminhttp.go's writeWebhookServiceError) can map them to 400 via
// errors.Is instead of every input mistake surfacing as a 500 — same
// pattern as services/gateway/internal/merchant/auth's ErrUnknownScope/ErrTooManyActiveKeys.
var (
	ErrTenantRequired     = fmt.Errorf("merchant/webhook: tenantID is required")
	ErrURLRequired        = fmt.Errorf("merchant/webhook: url is required")
	ErrInvalidURL         = fmt.Errorf("merchant/webhook: url must be an absolute http or https URL with a host")
	ErrInvalidEnvironment = fmt.Errorf("merchant/webhook: environment must be sandbox or live")
	ErrEventsRequired     = fmt.Errorf("merchant/webhook: at least one subscribed event is required")
)

// validateWebhookURL enforces the shape resolveAndDial/safeClient assume at
// dispatch time (an absolute http(s) URL with a host) at CREATION time
// instead — catching a merchant/operator typo immediately with a 400
// rather than silently accepting it and only discovering the problem when
// the relay worker's first delivery attempt fails days later. This is a
// FORMAT check only; it deliberately does not resolve DNS or reject
// private addresses — that live-mode-only SSRF enforcement stays in
// ssrf.go's resolveAndDial, re-checked at every dispatch (TM-16) because a
// hostname valid at creation can be rebound to a private address later.
func validateWebhookURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidURL, err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ErrInvalidURL
	}
	return nil
}

func secretAAD(endpointID uuid.UUID) cryptox.AAD {
	return cryptox.AAD{Service: "merchant", Table: "merchant_webhook_endpoints", Column: "secret", RowID: endpointID.String()}
}

// Service manages webhook endpoints — creation, rotation, listing — for
// the tenant-facing side of T7 (the relay worker, in relay.go, is the
// delivery side).
type Service struct {
	repo repository.WebhookRepository
	ring *cryptox.Ring
}

// NewService panics on a nil ring — T7 has no valid "cryptox unconfigured"
// mode to construct in, same posture as every other secret-at-rest field
// in this codebase (payin/payout's own NewRepository, A8 T2's own rule).
func NewService(repo repository.WebhookRepository, ring *cryptox.Ring) *Service {
	if repo == nil {
		panic("merchant/webhook: NewService requires a non-nil WebhookRepository")
	}
	if ring == nil {
		panic("merchant/webhook: NewService requires a non-nil cryptox ring")
	}
	return &Service{repo: repo, ring: ring}
}

// generateSecret returns raw, base64url-encoded secret bytes — the same
// encoding convention services/gateway/internal/merchant/auth's API keys already use.
func generateSecret() (string, error) {
	raw := make([]byte, secretByteLen)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("merchant/webhook: generate secret: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// CreateEndpoint provisions a new webhook endpoint for tenantID and
// returns the PLAINTEXT secret exactly once — callers must display and
// discard it; it is never retrievable again (mirrors
// services/gateway/internal/merchant/auth.KeyService.CreateKey's own one-time-display
// contract). environment is fixed for the endpoint's lifetime.
func (s *Service) CreateEndpoint(ctx context.Context, tenantID uuid.UUID, url, environment string, subscribedEvents []string, description *string) (model.WebhookEndpoint, string, error) {
	if tenantID == uuid.Nil {
		return model.WebhookEndpoint{}, "", ErrTenantRequired
	}
	if url == "" {
		return model.WebhookEndpoint{}, "", ErrURLRequired
	}
	if err := validateWebhookURL(url); err != nil {
		return model.WebhookEndpoint{}, "", err
	}
	if environment != "sandbox" && environment != "live" {
		return model.WebhookEndpoint{}, "", ErrInvalidEnvironment
	}
	if len(subscribedEvents) == 0 {
		return model.WebhookEndpoint{}, "", ErrEventsRequired
	}

	secret, err := generateSecret()
	if err != nil {
		return model.WebhookEndpoint{}, "", err
	}

	id := identifiers.NewV7()
	ciphertext, err := s.ring.Seal(secretAAD(id), []byte(secret))
	if err != nil {
		return model.WebhookEndpoint{}, "", fmt.Errorf("merchant/webhook: seal secret: %w", err)
	}

	endpoint := model.WebhookEndpoint{
		ID: id, PublicID: "wh_" + uuid.NewString()[:16], TenantID: tenantID,
		URL: url, Status: "enabled", SecretCiphertext: ciphertext, SecretVersion: s.ring.CurrentVersion(),
		SubscribedEvents: subscribedEvents, Environment: environment, Description: description,
	}
	if err := s.repo.CreateEndpoint(ctx, endpoint); err != nil {
		return model.WebhookEndpoint{}, "", fmt.Errorf("merchant/webhook: create endpoint: %w", err)
	}
	return endpoint, secret, nil
}

// RotateSecret replaces tenantID's endpointID's signing secret in place,
// returning the new PLAINTEXT secret exactly once — same one-time-display
// contract as CreateEndpoint. The endpoint's id/url/subscriptions are
// unchanged; only the secret material and its ciphertext version rotate.
func (s *Service) RotateSecret(ctx context.Context, tenantID, endpointID uuid.UUID) (string, error) {
	endpoint, err := s.repo.GetEndpoint(ctx, tenantID, endpointID)
	if err != nil {
		return "", err
	}
	secret, err := generateSecret()
	if err != nil {
		return "", err
	}
	ciphertext, err := s.ring.Seal(secretAAD(endpointID), []byte(secret))
	if err != nil {
		return "", fmt.Errorf("merchant/webhook: seal rotated secret: %w", err)
	}
	endpoint.SecretCiphertext = ciphertext
	endpoint.SecretVersion = s.ring.CurrentVersion()
	if err := s.repo.UpdateEndpoint(ctx, endpoint); err != nil {
		return "", fmt.Errorf("merchant/webhook: persist rotated secret: %w", err)
	}
	return secret, nil
}

func (s *Service) ListEndpoints(ctx context.Context, tenantID uuid.UUID) ([]model.WebhookEndpoint, error) {
	return s.repo.ListEndpoints(ctx, tenantID)
}

func (s *Service) GetEndpoint(ctx context.Context, tenantID, endpointID uuid.UUID) (model.WebhookEndpoint, error) {
	return s.repo.GetEndpoint(ctx, tenantID, endpointID)
}

func (s *Service) DeleteEndpoint(ctx context.Context, tenantID, endpointID uuid.UUID) error {
	return s.repo.DeleteEndpoint(ctx, tenantID, endpointID)
}

// SetEndpointStatus enables or disables tenantID's endpointID — the
// tenant-triggered counterpart of the relay's own automatic
// DisableEndpoint (410 auto-disable, relay.go).
func (s *Service) SetEndpointStatus(ctx context.Context, tenantID, endpointID uuid.UUID, status string) error {
	if status != "enabled" && status != "disabled" {
		return fmt.Errorf("merchant/webhook: status must be enabled or disabled, got %q", status)
	}
	endpoint, err := s.repo.GetEndpoint(ctx, tenantID, endpointID)
	if err != nil {
		return err
	}
	endpoint.Status = status
	return s.repo.UpdateEndpoint(ctx, endpoint)
}

// ListDeliveries/GetDelivery re-export the repository's own tenant-scoped
// reads so callers never need to import services/gateway/internal/merchant/repository
// directly (module-boundary convention already established by every
// other services/gateway/internal/merchant subpackage this session).
func (s *Service) ListDeliveries(ctx context.Context, tenantID uuid.UUID, limit int) ([]model.WebhookDelivery, error) {
	return s.repo.ListDeliveries(ctx, tenantID, limit)
}

func (s *Service) GetDelivery(ctx context.Context, tenantID, deliveryID uuid.UUID) (model.WebhookDelivery, error) {
	return s.repo.GetDelivery(ctx, tenantID, deliveryID)
}
