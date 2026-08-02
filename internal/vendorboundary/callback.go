package vendorboundary

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	payinv1 "github.com/herdifirdausss/seev/gen/payin/v1"
	payoutv1 "github.com/herdifirdausss/seev/gen/payout/v1"
	"github.com/herdifirdausss/seev/internal/vendorgw"
	"github.com/herdifirdausss/seev/pkg/cryptox"
	"github.com/herdifirdausss/seev/pkg/database"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const maxCallbackBodyBytes = 64 << 10

// NormalizedCallback is the only callback shape that leaves VendorService.
// It deliberately has no Seev user id.
type NormalizedCallback struct {
	Flow              string
	Vendor            string
	VendorEventID     string
	ExternalReference string
	Amount            string
	Currency          string
	Status            string
	OccurredAt        time.Time
	UnknownStatus     string
}

type CallbackVerifier interface {
	VerifyAndNormalize(http.Header, []byte) (*NormalizedCallback, error)
}

type PayinCallbackClient interface {
	HandleVendorCallback(context.Context, *payinv1.HandleVendorCallbackRequest, ...grpc.CallOption) (*payinv1.HandleVendorCallbackResponse, error)
}

type PayoutCallbackClient interface {
	HandleVendorCallback(context.Context, *payoutv1.HandleVendorCallbackRequest, ...grpc.CallOption) (*payoutv1.HandleVendorCallbackResponse, error)
}

type CallbackHandler struct {
	store    *InboxStore
	registry *Registry
	payin    PayinCallbackClient
	payout   PayoutCallbackClient
	allowed  []*net.IPNet
	trusted  []*net.IPNet
}

func NewCallbackHandler(db *database.DBSQL, ring *cryptox.Ring, registry *Registry, payin PayinCallbackClient, payout PayoutCallbackClient, allowedCIDRs, trustedProxyCIDRs string) (*CallbackHandler, error) {
	if ring == nil {
		return nil, fmt.Errorf("vendorboundary: NewCallbackHandler requires a non-nil cryptox ring")
	}
	allowed, err := parseCIDRs(allowedCIDRs, []string{"127.0.0.1/32", "::1/128"})
	if err != nil {
		return nil, fmt.Errorf("parse callback allowlist: %w", err)
	}
	trusted, err := parseCIDRs(trustedProxyCIDRs, nil)
	if err != nil {
		return nil, fmt.Errorf("parse callback trusted proxies: %w", err)
	}
	return &CallbackHandler{store: &InboxStore{db: db, ring: ring}, registry: registry, payin: payin, payout: payout, allowed: allowed, trusted: trusted}, nil
}

func (h *CallbackHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	vendor := r.PathValue("vendor")
	if !h.sourceAllowed(r) {
		vendorCallbackDenied.WithLabelValues("source_policy").Inc()
		w.WriteHeader(http.StatusForbidden)
		return
	}
	adapter, err := h.registry.callbackAdapter(vendor)
	if errors.Is(err, ErrUnknownVendor) {
		vendorCallbackDenied.WithLabelValues("unknown_vendor").Inc()
		http.NotFound(w, r)
		return
	}
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxCallbackBodyBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			vendorCallbackDenied.WithLabelValues("body_cap").Inc()
			w.WriteHeader(http.StatusRequestEntityTooLarge)
		} else {
			w.WriteHeader(http.StatusBadRequest)
		}
		return
	}
	headers := selectedHeaders(r.Header)
	normalized, err := adapter.VerifyAndNormalize(r.Header, body)
	if err != nil {
		if errors.Is(err, vendorgw.ErrInvalidSignature) {
			vendorCallbackDenied.WithLabelValues("signature").Inc()
			w.WriteHeader(http.StatusUnauthorized)
		} else {
			vendorCallbackDenied.WithLabelValues("malformed_body").Inc()
			w.WriteHeader(http.StatusBadRequest)
		}
		return
	}
	if normalized == nil {
		writeCallbackAck(w)
		return
	}
	if normalized.Vendor == "" {
		normalized.Vendor = vendor
	}
	if normalized.VendorEventID == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	record, err := h.store.Ensure(r.Context(), normalized, body, headers, sourcePolicy(r))
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	if record.outcome != "" && record.status != "received" && record.status != "retry" {
		writeCallbackAck(w)
		return
	}
	if !record.claimed {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	outcome, err := h.deliver(r.Context(), normalized, record.id.String(), r.Header.Get("X-Request-Id"))
	if err != nil {
		_ = h.store.Finish(r.Context(), record.id, "retry", "", err.Error())
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	status, outcomeName := callbackOutcome(outcome)
	vendorCallbackDeliveries.WithLabelValues(normalized.Vendor, status).Inc()
	if err := h.store.Finish(r.Context(), record.id, status, outcomeName, ""); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	writeCallbackAck(w)
}

func (h *CallbackHandler) deliver(ctx context.Context, callback *NormalizedCallback, inboxID, requestID string) (string, error) {
	ts := timestamppb.New(callback.OccurredAt)
	switch callback.Flow {
	case "payin":
		if h.payin == nil {
			return "", fmt.Errorf("payin callback owner unavailable")
		}
		response, err := h.payin.HandleVendorCallback(ctx, &payinv1.HandleVendorCallbackRequest{Vendor: callback.Vendor, VendorEventId: callback.VendorEventID, ExternalReference: callback.ExternalReference, Amount: callback.Amount, Currency: callback.Currency, Status: callback.Status, OccurredAt: ts, VendorInboxId: inboxID, RequestId: requestID, UnknownVendorStatus: callback.UnknownStatus})
		if err != nil {
			return "", err
		}
		return response.GetResult().String(), nil
	case "payout":
		if h.payout == nil {
			return "", fmt.Errorf("payout callback owner unavailable")
		}
		response, err := h.payout.HandleVendorCallback(ctx, &payoutv1.HandleVendorCallbackRequest{Vendor: callback.Vendor, VendorEventId: callback.VendorEventID, ExternalReference: callback.ExternalReference, Amount: callback.Amount, Currency: callback.Currency, Status: callback.Status, OccurredAt: ts, VendorInboxId: inboxID, RequestId: requestID, UnknownVendorStatus: callback.UnknownStatus})
		if err != nil {
			return "", err
		}
		return response.GetResult().String(), nil
	default:
		return "recorded_unmatched", nil
	}
}

func callbackOutcome(outcome string) (string, string) {
	switch {
	case strings.HasSuffix(outcome, "FINALIZED"):
		return "finalized", strings.ToLower(outcome)
	case strings.HasSuffix(outcome, "IGNORED_NON_TERMINAL"):
		return "ignored", strings.ToLower(outcome)
	default:
		return "unmatched", strings.ToLower(outcome)
	}
}

func writeCallbackAck(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"received":true}`))
}

func (h *CallbackHandler) sourceAllowed(r *http.Request) bool {
	peer, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		peer = r.RemoteAddr
	}
	peerIP := net.ParseIP(peer)
	if peerIP == nil {
		return false
	}
	if containsIP(h.trusted, peerIP) {
		for forwarded := range strings.SplitSeq(r.Header.Get("X-Forwarded-For"), ",") {
			if ip := net.ParseIP(strings.TrimSpace(forwarded)); ip != nil && containsIP(h.allowed, ip) {
				return true
			}
		}
		return false
	}
	return containsIP(h.allowed, peerIP)
}

func sourcePolicy(r *http.Request) string {
	return "cidr:" + r.RemoteAddr
}

func selectedHeaders(headers http.Header) map[string]string {
	selected := make(map[string]string)
	for _, key := range []string{"Content-Type", "X-Mock-Signature", "X-Request-Id"} {
		if value := headers.Get(key); value != "" {
			selected[key] = value
		}
	}
	return selected
}

func parseCIDRs(raw string, defaults []string) ([]*net.IPNet, error) {
	values := defaults
	if strings.TrimSpace(raw) != "" {
		values = strings.Split(raw, ",")
	}
	result := make([]*net.IPNet, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		_, network, err := net.ParseCIDR(value)
		if err != nil {
			return nil, fmt.Errorf("%q: %w", value, err)
		}
		result = append(result, network)
	}
	return result, nil
}

func containsIP(networks []*net.IPNet, ip net.IP) bool {
	for _, network := range networks {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

type inboxRecord struct {
	id      uuid.UUID
	status  string
	outcome string
	claimed bool
}

// InboxStore is the durable callback inbox. Raw bytes never leave this
// package except for the database insert, and never leave this process in
// plaintext at all — raw_body/selected_headers are sealed under ring before
// the INSERT (security audit finding: this was the one raw-payload column
// in the codebase with no cryptox protection).
type InboxStore struct {
	db   *database.DBSQL
	ring *cryptox.Ring
}

func rawBodyAAD(id uuid.UUID) cryptox.AAD {
	return cryptox.AAD{Service: "vendor", Table: "vendor_callback_inbox", Column: "raw_body", RowID: id.String()}
}

func selectedHeadersAAD(id uuid.UUID) cryptox.AAD {
	return cryptox.AAD{Service: "vendor", Table: "vendor_callback_inbox", Column: "selected_headers", RowID: id.String()}
}

func (s *InboxStore) Ensure(ctx context.Context, callback *NormalizedCallback, raw []byte, headers map[string]string, source string) (inboxRecord, error) {
	encodedHeaders, err := json.Marshal(headers)
	if err != nil {
		return inboxRecord{}, err
	}
	id := uuid.New()
	rawCiphertext, err := s.ring.Seal(rawBodyAAD(id), raw)
	if err != nil {
		return inboxRecord{}, fmt.Errorf("encrypt callback raw body: %w", err)
	}
	headersCiphertext, err := s.ring.Seal(selectedHeadersAAD(id), encodedHeaders)
	if err != nil {
		return inboxRecord{}, fmt.Errorf("encrypt callback headers: %w", err)
	}
	v := s.ring.CurrentVersion()
	result, err := s.db.ExecContext(ctx, `INSERT INTO vendor_callback_inbox
		(id, vendor, vendor_event_id, external_reference, amount, currency, normalized_status, unknown_vendor_status, occurred_at, raw_body_ciphertext, raw_body_key_version, selected_headers_ciphertext, selected_headers_key_version, source_policy)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14) ON CONFLICT (vendor, vendor_event_id) DO NOTHING`,
		id, callback.Vendor, callback.VendorEventID, callback.ExternalReference, callback.Amount, callback.Currency, callback.Status, callback.UnknownStatus, callback.OccurredAt, rawCiphertext, v, headersCiphertext, v, source)
	if err != nil {
		return inboxRecord{}, err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		row := s.db.QueryRowContext(ctx, `SELECT id, processing_status, outcome FROM vendor_callback_inbox WHERE vendor=$1 AND vendor_event_id=$2`, callback.Vendor, callback.VendorEventID)
		var record inboxRecord
		if err := row.Scan(&record.id, &record.status, &record.outcome); err != nil {
			return inboxRecord{}, err
		}
		if record.status == "received" || record.status == "retry" {
			return s.claim(ctx, record)
		}
		return record, nil
	}
	return s.claim(ctx, inboxRecord{id: id, status: "received"})
}

func (s *InboxStore) claim(ctx context.Context, record inboxRecord) (inboxRecord, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE vendor_callback_inbox SET processing_status='processing', attempts=attempts+1, updated_at=now() WHERE id=$1 AND processing_status IN ('received','retry')`, record.id)
	if err != nil {
		return inboxRecord{}, err
	}
	n, _ := result.RowsAffected()
	record.claimed = n == 1
	return record, nil
}

func (s *InboxStore) Finish(ctx context.Context, id uuid.UUID, status, outcome, reason string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE vendor_callback_inbox SET processing_status=$1, outcome=$2, updated_at=now() WHERE id=$3`, status, outcome, id)
	return err
}
