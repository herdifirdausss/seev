package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"
)

// OwnerClosureClient is the outbound side of docs/roadmap/active/51-a8-data-lifecycle-privacy.md
// K9/K10/K11's per-owner privacy contract — auth calls each registered
// owner's own endpoints (`POST <base>/privacy/closure/prepare`,
// `POST <base>/privacy/closure/commit`, `GET <base>/privacy/export`) over
// mTLS. A local structural interface (same convention as this package's
// own Provisioner) so the closure/export sagas never depend on the
// concrete HTTP transport, and integration tests can point it at an
// httptest.Server wrapping a real in-process owner module instead of a
// mock. Every owner (ledger, payin, payout, fraud, gateway) implements
// the identical wire contract, so ONE client type
// (httpOwnerClosureClient below) serves all of them, for both sagas —
// RegisterClosureOwner is what gives each instance its own name/base URL.
type OwnerClosureClient interface {
	Prepare(ctx context.Context, subjectID uuid.UUID) (blocked bool, reasons []string, err error)
	Commit(ctx context.Context, subjectID, surrogateID uuid.UUID) (resultHash string, affectedCount int, err error)
	// Export returns the owner's own rows for subjectID as of cutoff,
	// each already a complete JSON object (with its own "type" field) —
	// K9's own owner-composed export contract.
	Export(ctx context.Context, subjectID uuid.UUID, cutoff time.Time) ([]json.RawMessage, error)
}

type httpOwnerClosureClient struct {
	baseURL string
	token   string
	http    *http.Client
}

// NewOwnerClosureClient builds the mTLS + shared-internal-token client
// cmd/auth-service/main.go wires via RegisterClosureOwner, once per
// owner. httpClient is expected to already carry the mTLS transport bound
// to the target owner's identity (pkg/tlsx.HTTPClient(certSrc, identity,
// timeout)) — this type adds only the Authorization: Bearer header
// pkg/middleware.WithInternalToken on each owner's side checks, the same
// shared INTERNAL_GRPC_TOKEN secret already used for every other internal
// caller in this codebase (no new secret introduced for this feature).
func NewOwnerClosureClient(baseURL, internalToken string, httpClient *http.Client) OwnerClosureClient {
	return &httpOwnerClosureClient{baseURL: baseURL, token: internalToken, http: httpClient}
}

type closurePrepareWireRequest struct {
	SubjectID uuid.UUID `json:"subject_id"`
}

type closurePrepareWireResponse struct {
	Success bool `json:"success"`
	Data    struct {
		Blocked bool     `json:"blocked"`
		Reasons []string `json:"reasons,omitempty"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (c *httpOwnerClosureClient) Prepare(ctx context.Context, subjectID uuid.UUID) (bool, []string, error) {
	var resp closurePrepareWireResponse
	if err := c.doJSON(ctx, "/privacy/closure/prepare", closurePrepareWireRequest{SubjectID: subjectID}, &resp); err != nil {
		return false, nil, err
	}
	return resp.Data.Blocked, resp.Data.Reasons, nil
}

type closureCommitWireRequest struct {
	SubjectID   uuid.UUID `json:"subject_id"`
	SurrogateID uuid.UUID `json:"surrogate_id"`
}

type closureCommitWireResponse struct {
	Success bool `json:"success"`
	Data    struct {
		ResultHash    string `json:"result_hash"`
		AffectedCount int    `json:"affected_count"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (c *httpOwnerClosureClient) Commit(ctx context.Context, subjectID, surrogateID uuid.UUID) (string, int, error) {
	var resp closureCommitWireResponse
	if err := c.doJSON(ctx, "/privacy/closure/commit", closureCommitWireRequest{SubjectID: subjectID, SurrogateID: surrogateID}, &resp); err != nil {
		return "", 0, err
	}
	return resp.Data.ResultHash, resp.Data.AffectedCount, nil
}

type exportWireResponse struct {
	Success bool `json:"success"`
	Data    struct {
		Rows []json.RawMessage `json:"rows"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (c *httpOwnerClosureClient) Export(ctx context.Context, subjectID uuid.UUID, cutoff time.Time) ([]json.RawMessage, error) {
	// RFC3339Nano (not RFC3339) preserves sub-second precision on the wire —
	// RFC3339's Format drops fractional seconds entirely, which would make
	// every owner's cutoff silently earlier than the one privacy_requests
	// itself recorded (and earlier than auth's own in-process
	// collectAuthOwnerRows uses), excluding rows created in that
	// truncated sub-second gap from owner exports only.
	path := "/privacy/export?" + url.Values{
		"user_id": {subjectID.String()},
		"cutoff":  {cutoff.UTC().Format(time.RFC3339Nano)},
	}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("auth: build owner export request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrClosureOwnerUnavailable, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, fmt.Errorf("auth: read owner export response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: owner returned %d: %s", ErrClosureOwnerUnavailable, resp.StatusCode, string(data))
	}
	var wire exportWireResponse
	if err := json.Unmarshal(data, &wire); err != nil {
		return nil, fmt.Errorf("auth: decode owner export response: %w", err)
	}
	return wire.Data.Rows, nil
}

func (c *httpOwnerClosureClient) doJSON(ctx context.Context, path string, reqBody, respBody any) error {
	encoded, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("auth: encode owner closure request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(encoded))
	if err != nil {
		return fmt.Errorf("auth: build owner closure request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrClosureOwnerUnavailable, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("auth: read owner closure response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: owner returned %d: %s", ErrClosureOwnerUnavailable, resp.StatusCode, string(data))
	}
	if err := json.Unmarshal(data, respBody); err != nil {
		return fmt.Errorf("auth: decode owner closure response: %w", err)
	}
	return nil
}
