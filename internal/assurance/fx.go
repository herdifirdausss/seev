package assurance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/assurance/rules"
)

// FXReconciliationReader is the read-only Ledger boundary used by Assurance.
// It is intentionally an HTTP contract rather than a generated RPC extension:
// the FX proof is an internal operator/Assurance projection, not user data.
type FXReconciliationReader interface {
	ReconcileFXConversions(context.Context, time.Time, time.Time, int) (FXReconciliationReport, error)
}

type FXReconciliationReport struct {
	From       time.Time                  `json:"from"`
	To         time.Time                  `json:"to"`
	Total      int                        `json:"total"`
	Reconciled int                        `json:"reconciled"`
	Critical   int                        `json:"critical"`
	Items      []FXReconciliationEvidence `json:"items"`
}

type FXReconciliationEvidence struct {
	ResourceType          string    `json:"resource_type"`
	ResourceID            string    `json:"resource_id"`
	ConversionID          string    `json:"conversion_id"`
	QuoteID               string    `json:"quote_id"`
	SourceCurrency        string    `json:"source_currency"`
	TargetCurrency        string    `json:"target_currency"`
	SourceAmount          string    `json:"source_amount"`
	TargetAmount          string    `json:"target_amount"`
	SourceTransactionID   string    `json:"source_transaction_id"`
	TargetTransactionID   string    `json:"target_transaction_id"`
	SourceLegStatus       string    `json:"source_leg_status"`
	TargetLegStatus       string    `json:"target_leg_status"`
	SourceLinkValid       bool      `json:"source_link_valid"`
	TargetLinkValid       bool      `json:"target_link_valid"`
	SourceLegBalanced     bool      `json:"source_leg_balanced"`
	TargetLegBalanced     bool      `json:"target_leg_balanced"`
	QuoteValid            bool      `json:"quote_valid"`
	PositionAccountsValid bool      `json:"position_accounts_valid"`
	PositionBalancesValid bool      `json:"position_balances_valid"`
	AggregateEventPresent bool      `json:"aggregate_event_present"`
	Status                string    `json:"status"`
	Reason                string    `json:"reason"`
	CheckedAt             time.Time `json:"checked_at"`
}

type fxReconciliationHTTPClient struct {
	baseURL string
	token   string
	http    *http.Client
}

// NewFXReconciliationClient builds the mTLS + shared-token client used by the
// Assurance worker to read Ledger's bounded FX evidence endpoint.
func NewFXReconciliationClient(baseURL, internalToken string, httpClient *http.Client) FXReconciliationReader {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &fxReconciliationHTTPClient{baseURL: strings.TrimRight(baseURL, "/"), token: internalToken, http: httpClient}
}

func (c *fxReconciliationHTTPClient) ReconcileFXConversions(ctx context.Context, from, to time.Time, limit int) (FXReconciliationReport, error) {
	if c.baseURL == "" {
		return FXReconciliationReport{}, errors.New("ledger FX reconciliation URL is not configured")
	}
	query := url.Values{}
	if !from.IsZero() {
		query.Set("from", from.UTC().Format(time.RFC3339Nano))
	}
	if !to.IsZero() {
		query.Set("to", to.UTC().Format(time.RFC3339Nano))
	}
	if limit > 0 {
		query.Set("limit", fmt.Sprintf("%d", limit))
	}
	endpoint := c.baseURL + "/assurance/fx/reconciliation"
	if encoded := query.Encode(); encoded != "" {
		endpoint += "?" + encoded
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return FXReconciliationReport{}, fmt.Errorf("build ledger FX reconciliation request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return FXReconciliationReport{}, fmt.Errorf("ledger FX reconciliation request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return FXReconciliationReport{}, fmt.Errorf("read Ledger FX reconciliation response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return FXReconciliationReport{}, fmt.Errorf("ledger FX reconciliation returned %s: %s", resp.Status, bytes.TrimSpace(body))
	}
	var wire struct {
		Success bool                   `json:"success"`
		Data    FXReconciliationReport `json:"data"`
		Error   *struct {
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		return FXReconciliationReport{}, fmt.Errorf("decode ledger FX reconciliation response: %w", err)
	}
	if !wire.Success {
		if wire.Error != nil && wire.Error.Message != "" {
			return FXReconciliationReport{}, fmt.Errorf("ledger FX reconciliation failed: %s", wire.Error.Message)
		}
		return FXReconciliationReport{}, errors.New("ledger FX reconciliation failed")
	}
	return wire.Data, nil
}

func (m *Module) scanFX(ctx context.Context, runID uuid.UUID, cutoff time.Time, backfill bool) (int, error) {
	from := cutoff.Add(-24 * time.Hour)
	if backfill {
		from = cutoff.Add(-366 * 24 * time.Hour)
	}
	report, err := m.fx.ReconcileFXConversions(ctx, from, cutoff, 1000)
	if err != nil {
		return 0, fmt.Errorf("FX reconciliation: %w", err)
	}
	if len(report.Items) >= 1000 {
		return 0, errors.New("FX reconciliation report reached its bounded limit; refusing to resolve unseen findings")
	}
	seenByResource := make(map[string]map[string]bool)
	for _, item := range report.Items {
		if strings.TrimSpace(item.ResourceType) == "" {
			item.ResourceType = "conversion"
		}
		resourceID := strings.TrimSpace(item.ResourceID)
		if resourceID == "" {
			resourceID = strings.TrimSpace(item.ConversionID)
		}
		if resourceID == "" {
			return 0, errors.New("FX reconciliation returned an item without resource_id")
		}
		seen := seenByResource[resourceID]
		if seen == nil {
			seen = make(map[string]bool)
			seenByResource[resourceID] = seen
		}
		finding, ok, err := fxFinding(item, resourceID)
		if err != nil {
			return 0, err
		}
		if !ok {
			continue
		}
		seen[finding.Fingerprint] = true
		opened, err := m.upsertFinding(ctx, finding, time.Now(), backfill)
		if err != nil {
			return 0, fmt.Errorf("persist FX finding: %w", err)
		}
		if opened {
			if err := m.incrementRunFindings(ctx, runID); err != nil {
				return 0, fmt.Errorf("record FX finding transition: %w", err)
			}
		}
	}
	for resourceID, seen := range seenByResource {
		if err := m.resolveResourceFindings(ctx, resourceID, seen); err != nil {
			return 0, fmt.Errorf("resolve FX findings: %w", err)
		}
	}
	if err := m.recordPage(ctx, runID); err != nil {
		return 0, err
	}
	recordsScanned.WithLabelValues("fx").Add(float64(len(report.Items)))
	return len(report.Items), nil
}

func fxFinding(item FXReconciliationEvidence, resourceID string) (Finding, bool, error) {
	if item.Status == "reconciled" || item.Status == "failed" {
		return Finding{}, false, nil
	}
	amountRaw := item.SourceAmount
	currency := item.SourceCurrency
	if amountRaw == "" || strings.TrimSpace(currency) == "" {
		amountRaw = item.TargetAmount
		currency = item.TargetCurrency
	}
	amount, err := rules.ParseMinor(amountRaw)
	if err != nil {
		return Finding{}, false, fmt.Errorf("FX amount for %s: %w", resourceID, err)
	}
	ruleCode := "FX01"
	severity := "critical"
	if item.Status == "pending" {
		ruleCode = "FX02"
	}
	reason := item.Reason
	if reason == "" {
		reason = "FX conversion reconciliation discrepancy"
	}
	evidence := map[string]string{
		"resource_type":           item.ResourceType,
		"resource_id":             resourceID,
		"status":                  item.Status,
		"reason":                  reason,
		"quote_id":                item.QuoteID,
		"source_currency":         item.SourceCurrency,
		"target_currency":         item.TargetCurrency,
		"source_amount":           item.SourceAmount,
		"target_amount":           item.TargetAmount,
		"source_leg_status":       item.SourceLegStatus,
		"target_leg_status":       item.TargetLegStatus,
		"source_link_valid":       fmt.Sprintf("%t", item.SourceLinkValid),
		"target_link_valid":       fmt.Sprintf("%t", item.TargetLinkValid),
		"source_leg_balanced":     fmt.Sprintf("%t", item.SourceLegBalanced),
		"target_leg_balanced":     fmt.Sprintf("%t", item.TargetLegBalanced),
		"quote_valid":             fmt.Sprintf("%t", item.QuoteValid),
		"position_accounts_valid": fmt.Sprintf("%t", item.PositionAccountsValid),
		"position_balances_valid": fmt.Sprintf("%t", item.PositionBalancesValid),
		"aggregate_event_present": fmt.Sprintf("%t", item.AggregateEventPresent),
	}
	return Finding{
		Fingerprint: fxFindingFingerprint(ruleCode, item.ResourceType, resourceID),
		Severity:    severity,
		RuleCode:    ruleCode,
		ResourceID:  resourceID,
		AmountMinor: amount,
		Currency:    currency,
		Evidence:    evidence,
	}, true, nil
}

func fxFindingFingerprint(ruleCode, resourceType, resourceID string) string {
	if resourceType == "" || resourceType == "conversion" {
		return ruleCode + ":" + resourceID
	}
	return ruleCode + ":" + resourceType + ":" + resourceID
}
