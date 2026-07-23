package assurance

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	ledgerv1 "github.com/herdifirdausss/seev/gen/ledger/v1"
	payinv1 "github.com/herdifirdausss/seev/gen/payin/v1"
	payoutv1 "github.com/herdifirdausss/seev/gen/payout/v1"
	assurancerules "github.com/herdifirdausss/seev/internal/assurance/rules"
)

func (m *Module) scanPayin(ctx context.Context, runID uuid.UUID, cutoff time.Time, backfill bool) (int, error) {
	if m.payin == nil {
		return 0, errors.New("payin assurance client is unavailable")
	}
	cur, err := m.cursor(ctx, "payin")
	if err != nil {
		return 0, err
	}
	total := 0
	for {
		rpcCtx, cancel := context.WithTimeout(ctx, m.cfg.RPCTimeout)
		request := &payinv1.ListAssuranceRecordsRequest{PageSize: uint32(m.cfg.PageSize), Cutoff: timestamppb.New(cutoff)}
		if cur.Valid {
			request.CursorUpdatedAt = timestamppb.New(cur.UpdatedAt)
			request.CursorId = cur.ID.String()
		}
		response, callErr := m.payin.ListAssuranceRecords(rpcCtx, request)
		cancel()
		if callErr != nil {
			return total, fmt.Errorf("payin assurance RPC: %w", callErr)
		}
		if response == nil {
			return total, errors.New("payin assurance RPC returned nil response")
		}
		if err := validatePayinPage(response.GetRecords(), cutoff); err != nil {
			return total, err
		}
		if len(response.GetRecords()) == 0 && response.GetHasMore() {
			return total, errors.New("payin assurance RPC reported has_more with an empty page")
		}
		if cur.Valid && len(response.GetRecords()) > 0 {
			firstTime, firstID, err := payinCursor(response.GetRecords()[0])
			if err != nil {
				return total, err
			}
			if firstTime.Before(cur.UpdatedAt) || (firstTime.Equal(cur.UpdatedAt) && firstID.String() <= cur.ID.String()) {
				return total, errors.New("payin assurance RPC did not advance cursor")
			}
		}
		if err := m.provePayin(ctx, response.GetRecords(), backfill, runID); err != nil {
			return total, err
		}
		total += len(response.GetRecords())
		if len(response.GetRecords()) > 0 {
			last := response.GetRecords()[len(response.GetRecords())-1]
			lastUpdated, lastID, err := payinCursor(last)
			if err != nil {
				return total, err
			}
			if err := m.recordPage(ctx, runID); err != nil {
				return total, err
			}
			if err := m.advanceCursor(ctx, "payin", lastUpdated, lastID.String(), runID, backfill && !response.GetHasMore()); err != nil {
				return total, err
			}
			cur = cursorValue{Valid: true, UpdatedAt: lastUpdated, ID: lastID}
		}
		if !response.GetHasMore() {
			break
		}
	}
	if total == 0 && backfill {
		if err := m.markBackfillComplete(ctx, "payin", runID); err != nil {
			return total, err
		}
	}
	if total == 0 {
		if err := m.recordPage(ctx, runID); err != nil {
			return total, err
		}
	}
	recordsScanned.WithLabelValues("payin").Add(float64(total))
	return total, nil
}

func (m *Module) scanPayout(ctx context.Context, runID uuid.UUID, cutoff time.Time, backfill bool) (int, error) {
	if m.payout == nil {
		return 0, errors.New("payout assurance client is unavailable")
	}
	cur, err := m.cursor(ctx, "payout")
	if err != nil {
		return 0, err
	}
	total := 0
	for {
		rpcCtx, cancel := context.WithTimeout(ctx, m.cfg.RPCTimeout)
		request := &payoutv1.ListAssuranceRecordsRequest{PageSize: uint32(m.cfg.PageSize), Cutoff: timestamppb.New(cutoff)}
		if cur.Valid {
			request.CursorUpdatedAt = timestamppb.New(cur.UpdatedAt)
			request.CursorId = cur.ID.String()
		}
		response, callErr := m.payout.ListAssuranceRecords(rpcCtx, request)
		cancel()
		if callErr != nil {
			return total, fmt.Errorf("payout assurance RPC: %w", callErr)
		}
		if response == nil {
			return total, errors.New("payout assurance RPC returned nil response")
		}
		if err := validatePayoutPage(response.GetRecords(), cutoff); err != nil {
			return total, err
		}
		if len(response.GetRecords()) == 0 && response.GetHasMore() {
			return total, errors.New("payout assurance RPC reported has_more with an empty page")
		}
		if cur.Valid && len(response.GetRecords()) > 0 {
			firstTime, firstID, err := payoutCursor(response.GetRecords()[0])
			if err != nil {
				return total, err
			}
			if firstTime.Before(cur.UpdatedAt) || (firstTime.Equal(cur.UpdatedAt) && firstID.String() <= cur.ID.String()) {
				return total, errors.New("payout assurance RPC did not advance cursor")
			}
		}
		if err := m.provePayout(ctx, response.GetRecords(), backfill, runID); err != nil {
			return total, err
		}
		total += len(response.GetRecords())
		if len(response.GetRecords()) > 0 {
			last := response.GetRecords()[len(response.GetRecords())-1]
			lastUpdated, lastID, err := payoutCursor(last)
			if err != nil {
				return total, err
			}
			if err := m.recordPage(ctx, runID); err != nil {
				return total, err
			}
			if err := m.advanceCursor(ctx, "payout", lastUpdated, lastID.String(), runID, backfill && !response.GetHasMore()); err != nil {
				return total, err
			}
			cur = cursorValue{Valid: true, UpdatedAt: lastUpdated, ID: lastID}
		}
		if !response.GetHasMore() {
			break
		}
	}
	if total == 0 && backfill {
		if err := m.markBackfillComplete(ctx, "payout", runID); err != nil {
			return total, err
		}
	}
	if total == 0 {
		if err := m.recordPage(ctx, runID); err != nil {
			return total, err
		}
	}
	recordsScanned.WithLabelValues("payout").Add(float64(total))
	return total, nil
}

func (m *Module) provePayin(ctx context.Context, records []*payinv1.AssuranceRecord, suppressAlerts bool, runID uuid.UUID) error {
	if len(records) == 0 {
		return nil
	}
	if m.ledger == nil {
		return errors.New("ledger assurance client is unavailable")
	}
	selectors := make([]*ledgerv1.AssuranceSelector, 0, len(records))
	for _, record := range records {
		if record.GetLedgerType() == "" || record.GetLedgerGateway() == "" || record.GetLedgerExternalRef() == "" {
			continue
		}
		selectors = append(selectors, &ledgerv1.AssuranceSelector{Token: record.GetId(), Type: record.GetLedgerType(), Gateway: record.GetLedgerGateway(), ExternalRef: record.GetLedgerExternalRef()})
	}
	response, err := m.batchLedger(ctx, selectors)
	if err != nil {
		return err
	}
	proofByToken, err := transactionProofs(response)
	if err != nil {
		return err
	}
	byID := make(map[string]*payinv1.AssuranceRecord, len(records))
	for _, record := range records {
		byID[record.GetId()] = record
	}
	for _, record := range records {
		amount, parseErr := assurancerules.ParseMinor(record.GetAmount())
		if parseErr != nil {
			return parseErr
		}
		value := assurancerules.PayinRecord{ID: record.GetId(), RecordType: record.GetRecordType(), Status: record.GetStatus(), UserID: record.GetUserId(), AmountMinor: amount, Currency: record.GetCurrency(), Vendor: record.GetVendor(), Reference: record.GetReference(), ExternalRef: record.GetExternalRef(), SettledEventID: record.GetSettledEventId(), RequestIDPresent: record.GetRequestIdPresent(), Age: time.Since(record.GetCreatedAt().AsTime()), Ledger: proofByToken[record.GetId()], ConsistencyDelay: m.cfg.ConsistencyDelay}
		if linked := byID[record.GetSettledEventId()]; linked != nil {
			linkedAmount, linkedErr := assurancerules.ParseMinor(linked.GetAmount())
			if linkedErr != nil {
				return linkedErr
			}
			value.SettledWebhook = &assurancerules.PayinRecord{ID: linked.GetId(), RecordType: linked.GetRecordType(), Status: linked.GetStatus(), UserID: linked.GetUserId(), AmountMinor: linkedAmount, Currency: linked.GetCurrency(), Reference: linked.GetReference(), ExternalRef: linked.GetExternalRef()}
		}
		seen := map[string]bool{}
		for _, finding := range assurancerules.EvaluatePayin(value) {
			seen[finding.Fingerprint] = true
			opened, err := m.upsertFinding(ctx, Finding{Fingerprint: finding.Fingerprint, Severity: finding.Severity, RuleCode: finding.RuleCode, ResourceID: finding.ResourceID, AmountMinor: finding.AmountMinor, Currency: finding.Currency, Evidence: finding.Evidence}, time.Now(), suppressAlerts)
			if err != nil {
				return fmt.Errorf("persist payin finding: %w", err)
			}
			if opened {
				if err := m.incrementRunFindings(ctx, runID); err != nil {
					return fmt.Errorf("record payin finding transition: %w", err)
				}
			}
		}
		if err := m.resolveResourceFindings(ctx, record.GetId(), seen); err != nil {
			return fmt.Errorf("resolve payin findings: %w", err)
		}
	}
	if err := m.advanceLedgerCursor(ctx, response, runID); err != nil {
		return err
	}
	return nil
}

func (m *Module) provePayout(ctx context.Context, records []*payoutv1.AssuranceRecord, suppressAlerts bool, runID uuid.UUID) error {
	if len(records) == 0 {
		return nil
	}
	if m.ledger == nil {
		return errors.New("ledger assurance client is unavailable")
	}
	selectors := make([]*ledgerv1.AssuranceSelector, 0, len(records)*2)
	for _, record := range records {
		for _, id := range []string{record.GetHoldTxId(), record.GetSettleTxId()} {
			if id == "" {
				continue
			}
			selectors = append(selectors, &ledgerv1.AssuranceSelector{Token: record.GetId(), TransactionId: id})
		}
	}
	response, err := m.batchLedger(ctx, selectors)
	if err != nil {
		return err
	}
	proofByToken, err := transactionProofs(response)
	if err != nil {
		return err
	}
	quoteIDs := make([]string, 0, len(records))
	for _, record := range records {
		if record.GetFeeQuoteId() != "" {
			quoteIDs = append(quoteIDs, record.GetFeeQuoteId())
		}
	}
	feeProofs, err := m.batchLedgerFeeQuotes(ctx, quoteIDs)
	if err != nil {
		return err
	}
	for _, record := range records {
		amount, parseErr := assurancerules.ParseMinor(record.GetAmount())
		if parseErr != nil {
			return parseErr
		}
		value := assurancerules.PayoutRecord{ID: record.GetId(), Status: record.GetStatus(), AmountMinor: amount, Currency: record.GetCurrency(), Vendor: record.GetVendor(), HoldTxID: record.GetHoldTxId(), SettleTxID: record.GetSettleTxId(), FeeQuoteID: record.GetFeeQuoteId(), FeeAmountMinor: parseMinorOrZero(record.GetFeeAmount()), FeeGateway: record.GetFeeGateway(), RequestIDPresent: record.GetRequestIdPresent(), Age: time.Since(record.GetCreatedAt().AsTime())}
		for _, proof := range proofByToken[record.GetId()] {
			if proof.ID == record.GetHoldTxId() {
				copy := proof
				value.Hold = &copy
			}
			if proof.ID == record.GetSettleTxId() {
				copy := proof
				value.Closing = &copy
				value.BookedFeeMinor = proof.BookedFeeMinor
				value.BookedFeeGateway = proof.BookedFeeGateway
			}
		}
		for _, call := range record.GetVendorCalls() {
			value.VendorCalls = append(value.VendorCalls, assurancerules.VendorCall{Attempt: int(call.GetAttempt()), Vendor: call.GetVendor(), Outcome: call.GetOutcome(), At: call.GetCreatedAt().AsTime()})
		}
		for _, command := range record.GetVendorCommands() {
			value.VendorCommands = append(value.VendorCommands, assurancerules.VendorCommand{ID: command.GetId(), Vendor: command.GetVendor(), Attempt: int(command.GetAttempt()), Status: command.GetStatus()})
		}
		if fee, ok := feeProofs[record.GetFeeQuoteId()]; ok {
			value.FeeQuote = &assurancerules.FeeProof{Exists: true, ConsumedByRef: fee.GetConsumedByRef(), AmountMinor: parseMinorOrZero(fee.GetFeeAmount()), Gateway: fee.GetFeeGateway(), TransactionType: fee.GetTransactionType()}
		}
		seen := map[string]bool{}
		for _, finding := range assurancerules.EvaluatePayout(value) {
			seen[finding.Fingerprint] = true
			opened, err := m.upsertFinding(ctx, Finding{Fingerprint: finding.Fingerprint, Severity: finding.Severity, RuleCode: finding.RuleCode, ResourceID: finding.ResourceID, AmountMinor: finding.AmountMinor, Currency: finding.Currency, Evidence: finding.Evidence}, time.Now(), suppressAlerts)
			if err != nil {
				return fmt.Errorf("persist payout finding: %w", err)
			}
			if opened {
				if err := m.incrementRunFindings(ctx, runID); err != nil {
					return fmt.Errorf("record payout finding transition: %w", err)
				}
			}
		}
		if err := m.resolveResourceFindings(ctx, record.GetId(), seen); err != nil {
			return fmt.Errorf("resolve payout findings: %w", err)
		}
	}
	if err := m.advanceLedgerCursor(ctx, response, runID); err != nil {
		return err
	}
	return nil
}

func (m *Module) batchLedger(ctx context.Context, selectors []*ledgerv1.AssuranceSelector) (*ledgerv1.BatchGetAssuranceTransactionsResponse, error) {
	combined := &ledgerv1.BatchGetAssuranceTransactionsResponse{}
	for start := 0; start < len(selectors); start += 500 {
		end := start + 500
		if end > len(selectors) {
			end = len(selectors)
		}
		rpcCtx, cancel := context.WithTimeout(ctx, m.cfg.RPCTimeout)
		response, err := m.ledger.BatchGetAssuranceTransactions(rpcCtx, &ledgerv1.BatchGetAssuranceTransactionsRequest{Selectors: selectors[start:end]})
		cancel()
		if err != nil {
			return nil, fmt.Errorf("ledger assurance RPC: %w", err)
		}
		combined.Results = append(combined.Results, response.GetResults()...)
	}
	return combined, nil
}

func (m *Module) batchLedgerFeeQuotes(ctx context.Context, quoteIDs []string) (map[string]*ledgerv1.FeeQuoteProof, error) {
	result := make(map[string]*ledgerv1.FeeQuoteProof)
	for start := 0; start < len(quoteIDs); start += 500 {
		end := start + 500
		if end > len(quoteIDs) {
			end = len(quoteIDs)
		}
		rpcCtx, cancel := context.WithTimeout(ctx, m.cfg.RPCTimeout)
		response, err := m.ledger.BatchGetAssuranceTransactions(rpcCtx, &ledgerv1.BatchGetAssuranceTransactionsRequest{FeeQuoteIds: quoteIDs[start:end]})
		cancel()
		if err != nil {
			return nil, fmt.Errorf("ledger fee quote assurance RPC: %w", err)
		}
		for _, proof := range response.GetFeeQuoteProofs() {
			result[proof.GetQuoteId()] = proof
		}
	}
	return result, nil
}

func validatePayinPage(records []*payinv1.AssuranceRecord, cutoff time.Time) error {
	var previous time.Time
	var previousID string
	for _, record := range records {
		updated, id, err := payinCursor(record)
		if err != nil {
			return err
		}
		if updated.After(cutoff) {
			return fmt.Errorf("payin assurance record %s is newer than cutoff", id)
		}
		if !previous.IsZero() && (updated.Before(previous) || (updated.Equal(previous) && id.String() <= previousID)) {
			return fmt.Errorf("payin assurance page is not strictly ordered")
		}
		if record.GetCreatedAt() == nil || !record.GetCreatedAt().IsValid() {
			return fmt.Errorf("payin assurance record %s has invalid created_at", id)
		}
		previous, previousID = updated, id.String()
	}
	return nil
}

func validatePayoutPage(records []*payoutv1.AssuranceRecord, cutoff time.Time) error {
	var previous time.Time
	var previousID string
	for _, record := range records {
		updated, id, err := payoutCursor(record)
		if err != nil {
			return err
		}
		if updated.After(cutoff) {
			return fmt.Errorf("payout assurance record %s is newer than cutoff", id)
		}
		if !previous.IsZero() && (updated.Before(previous) || (updated.Equal(previous) && id.String() <= previousID)) {
			return fmt.Errorf("payout assurance page is not strictly ordered")
		}
		if record.GetCreatedAt() == nil || !record.GetCreatedAt().IsValid() {
			return fmt.Errorf("payout assurance record %s has invalid created_at", id)
		}
		previous, previousID = updated, id.String()
	}
	return nil
}

func payinCursor(record *payinv1.AssuranceRecord) (time.Time, uuid.UUID, error) {
	if record == nil || record.GetEffectiveUpdatedAt() == nil || !record.GetEffectiveUpdatedAt().IsValid() {
		return time.Time{}, uuid.Nil, errors.New("payin assurance record has invalid effective_updated_at")
	}
	id, err := uuid.Parse(record.GetId())
	if err != nil {
		return time.Time{}, uuid.Nil, fmt.Errorf("payin assurance record id: %w", err)
	}
	return record.GetEffectiveUpdatedAt().AsTime(), id, nil
}

func payoutCursor(record *payoutv1.AssuranceRecord) (time.Time, uuid.UUID, error) {
	if record == nil || record.GetEffectiveUpdatedAt() == nil || !record.GetEffectiveUpdatedAt().IsValid() {
		return time.Time{}, uuid.Nil, errors.New("payout assurance record has invalid effective_updated_at")
	}
	id, err := uuid.Parse(record.GetId())
	if err != nil {
		return time.Time{}, uuid.Nil, fmt.Errorf("payout assurance record id: %w", err)
	}
	return record.GetEffectiveUpdatedAt().AsTime(), id, nil
}

func transactionProofs(response *ledgerv1.BatchGetAssuranceTransactionsResponse) (map[string][]assurancerules.LedgerProof, error) {
	proofs := make(map[string][]assurancerules.LedgerProof)
	for _, result := range response.GetResults() {
		for _, tx := range result.GetTransactions() {
			if tx == nil || tx.GetUpdatedAt() == nil || !tx.GetUpdatedAt().IsValid() {
				return nil, errors.New("ledger assurance transaction has invalid updated_at")
			}
			amount, err := assurancerules.ParseMinor(tx.GetAmount())
			if err != nil {
				return nil, err
			}
			bookedFee, err := assurancerules.ParseMinor(tx.GetBookedFeeAmount())
			if err != nil {
				bookedFee = 0
			}
			proofs[result.GetToken()] = append(proofs[result.GetToken()], assurancerules.LedgerProof{ID: tx.GetId(), Type: tx.GetType(), Status: tx.GetStatus(), AmountMinor: amount, Currency: tx.GetCurrency(), Gateway: tx.GetGateway(), ExternalRef: tx.GetExternalRef(), OriginalReferenceID: tx.GetOriginalReferenceId(), LifecycleCloserID: tx.GetLifecycleCloserId(), BookedFeeMinor: bookedFee, BookedFeeGateway: tx.GetBookedFeeGateway()})
		}
	}
	return proofs, nil
}

func parseMinorOrZero(value string) int64 {
	amount, err := assurancerules.ParseMinor(value)
	if err != nil {
		return 0
	}
	return amount
}
