// Package dispute implements chargeback case management (business-
// completeness audit finding: services/ledger/internal/processors/chargeback.go only
// ever posted the forced-debit money movement, with no queryable case
// record — no status lifecycle, no evidence deadline, no way to list open
// disputes). This package owns the case's lifecycle only; it never moves
// money itself — posting the `chargeback` transaction and calling
// LinkChargebackTx once it lands are separate ops steps this package
// coordinates but does not perform, the same separation recon.Service
// already established between ResolveItem (creates a pending adjustment)
// and the adjustment actually posting.
package dispute

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/herdifirdausss/seev/services/ledger/internal/ledger/errors"
	"github.com/herdifirdausss/seev/services/ledger/internal/ledger/model"
	"github.com/herdifirdausss/seev/services/ledger/internal/repository"
)

// validCardNetworks mirrors the migration's own CHECK constraint
// (services/ledger/migrations/000035_chargeback_disputes.up.sql) and the chargeback
// processor's doc comment — kept here too so a bad network is rejected with
// a client-safe apperror.ErrValidation instead of a raw constraint-violation
// error surfacing from the INSERT.
var validCardNetworks = map[string]bool{"visa": true, "mastercard": true, "jcb": true, "amex": true}

// OriginalTxReader is the narrow read used to validate the disputed charge
// exists, is posted, and to check its currency — a local structural
// interface (matches this codebase's own established pattern, e.g.
// recon.AdjustmentCreator) rather than depending on the full
// repository.TransactionRepository.
type OriginalTxReader interface {
	GetByID(ctx context.Context, transactionID uuid.UUID) (model.LedgerTransaction, error)
}

type Service struct {
	repo   repository.ChargebackDisputeRepository
	txRepo OriginalTxReader
}

func New(repo repository.ChargebackDisputeRepository, txRepo OriginalTxReader) *Service {
	return &Service{repo: repo, txRepo: txRepo}
}

// OpenDispute creates a new case against a posted charge. evidenceDueAt is
// caller-supplied (the card network's own dispute window varies by network
// and reason code — this package doesn't hardcode one).
func (s *Service) OpenDispute(ctx context.Context, originalTxID uuid.UUID, disputeRef, cardNetwork, reasonCode string,
	amount decimal.Decimal, currency string, evidenceDueAt *time.Time, createdBy string) (uuid.UUID, error) {
	if disputeRef == "" {
		return uuid.Nil, fmt.Errorf("%w: dispute_ref is required", apperror.ErrValidation)
	}
	if !validCardNetworks[cardNetwork] {
		return uuid.Nil, fmt.Errorf("%w: card_network must be one of visa/mastercard/jcb/amex, got %q", apperror.ErrValidation, cardNetwork)
	}
	if !amount.IsPositive() || !amount.Equal(amount.Truncate(0)) {
		return uuid.Nil, fmt.Errorf("%w: amount must be a positive integer", apperror.ErrValidation)
	}
	if createdBy == "" {
		return uuid.Nil, fmt.Errorf("%w: created_by (caller identity) is required", apperror.ErrValidation)
	}
	if evidenceDueAt != nil && !evidenceDueAt.After(time.Now().UTC()) {
		return uuid.Nil, fmt.Errorf("%w: evidence_due_at must be in the future", apperror.ErrValidation)
	}

	original, err := s.txRepo.GetByID(ctx, originalTxID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("%w: original transaction %s: %v", apperror.ErrOriginalNotFound, originalTxID, err)
	}
	if original.Status != "posted" {
		return uuid.Nil, fmt.Errorf("%w: original transaction status is %q, must be 'posted'", apperror.ErrNotReversible, original.Status)
	}
	if !original.Amount.IsPositive() || amount.GreaterThan(original.Amount) {
		return uuid.Nil, fmt.Errorf("%w: dispute amount %s exceeds original transaction amount %s", apperror.ErrValidation, amount, original.Amount)
	}
	if currency == "" {
		currency = original.Currency
	} else if currency != original.Currency {
		return uuid.Nil, fmt.Errorf("%w: dispute currency %q does not match original transaction currency %q", apperror.ErrCurrencyMismatch, currency, original.Currency)
	}

	d := model.ChargebackDispute{
		ID: uuid.New(), OriginalTxID: originalTxID, DisputeRef: disputeRef,
		CardNetwork: cardNetwork, ReasonCode: reasonCode, Amount: amount,
		Currency: currency, EvidenceDueAt: evidenceDueAt, CreatedBy: createdBy,
	}
	if err := s.repo.CreateDispute(ctx, d); err != nil {
		return uuid.Nil, err
	}
	return d.ID, nil
}

// ExpireDueDisputes closes cases whose evidence deadline has passed. The
// repository performs one locked, status-guarded transition per row and logs
// each transition, so worker retries are harmless.
func (s *Service) ExpireDueDisputes(ctx context.Context, now time.Time, actor, reason string) (int64, error) {
	if actor == "" {
		actor = "ledger-dispute-expiry-worker"
	}
	if reason == "" {
		reason = "evidence deadline expired"
	}
	expirer, ok := s.repo.(interface {
		ExpireDueDisputes(context.Context, time.Time, string, string) (int64, error)
	})
	if !ok {
		return 0, fmt.Errorf("%w: dispute expiry repository is unavailable", apperror.ErrValidation)
	}
	return expirer.ExpireDueDisputes(ctx, now, actor, reason)
}

func (s *Service) GetDispute(ctx context.Context, id uuid.UUID) (model.ChargebackDispute, error) {
	return s.repo.GetDispute(ctx, id)
}

func (s *Service) GetDisputeByRef(ctx context.Context, disputeRef string) (model.ChargebackDispute, error) {
	return s.repo.GetDisputeByRef(ctx, disputeRef)
}

func (s *Service) ListDisputesForTransaction(ctx context.Context, originalTxID uuid.UUID) ([]model.ChargebackDispute, error) {
	return s.repo.ListDisputesByOriginalTx(ctx, originalTxID)
}

// defaultListLimit/maxListLimit bound ListOpenDisputes' page — same pattern
// as recon.Service.GetBatchReport.
const (
	defaultListLimit = 100
	maxListLimit     = 500
)

func (s *Service) ListOpenDisputes(ctx context.Context, limit, offset int) ([]model.ChargebackDispute, error) {
	if limit <= 0 || limit > maxListLimit {
		limit = defaultListLimit
	}
	if offset < 0 {
		offset = 0
	}
	return s.repo.ListOpenDisputes(ctx, limit, offset)
}

// SubmitEvidence records the ops team's evidence package reference and
// moves the case from 'open' to 'evidence_submitted'. It does not accept a
// case already past 'open' — a second evidence submission on the same case
// is a new case (re-presentment), not a mutation of this one. changedBy is
// recorded in the status-change audit trail (security audit finding).
func (s *Service) SubmitEvidence(ctx context.Context, id uuid.UUID, evidenceRef, changedBy string) error {
	if evidenceRef == "" {
		return fmt.Errorf("%w: evidence_ref is required", apperror.ErrValidation)
	}
	if changedBy == "" {
		return fmt.Errorf("%w: changed_by (caller identity) is required", apperror.ErrValidation)
	}
	rows, err := s.repo.SubmitEvidence(ctx, id, evidenceRef, changedBy)
	if err != nil {
		return err
	}
	if rows == 0 {
		return s.diagnoseNoRows(ctx, id)
	}
	return nil
}

// resolvableStatuses are the only terminal outcomes ResolveDispute accepts —
// the card network's own decision, recorded by an ops identity.
var resolvableStatuses = map[string]bool{"won": true, "lost": true, "expired": true}

// ResolveDispute closes a case with a terminal outcome. reason and
// resolvedBy are both required — "a human must be able to say who and why"
// (security audit finding: resolution previously recorded neither actor
// nor an append-only transition history, unlike auth's kyc_level_changes).
func (s *Service) ResolveDispute(ctx context.Context, id uuid.UUID, status, resolvedBy, reason string) error {
	if !resolvableStatuses[status] {
		return fmt.Errorf("%w: status must be won/lost/expired, got %q", apperror.ErrValidation, status)
	}
	if resolvedBy == "" {
		return fmt.Errorf("%w: resolved_by (caller identity) is required", apperror.ErrValidation)
	}
	if reason == "" {
		return fmt.Errorf("%w: resolution reason is required", apperror.ErrValidation)
	}
	current, err := s.repo.GetDispute(ctx, id)
	if err != nil {
		return err
	}
	if current.CreatedBy == resolvedBy {
		return fmt.Errorf("%w: dispute creator cannot resolve the same case", apperror.ErrValidation)
	}
	rows, err := s.repo.ResolveDispute(ctx, id, status, resolvedBy, reason)
	if err != nil {
		return err
	}
	if rows == 0 {
		return s.diagnoseNoRows(ctx, id)
	}
	return nil
}

// ListStatusChanges returns a case's full transition history, oldest first.
func (s *Service) ListStatusChanges(ctx context.Context, disputeID uuid.UUID) ([]model.ChargebackDisputeStatusChange, error) {
	return s.repo.ListStatusChanges(ctx, disputeID)
}

// LinkChargebackTx records the chargeback processor's transaction id once
// its forced-debit posts. Callable at any point in the case's lifecycle
// (before or after resolution) — the money movement and the case decision
// are independent events in practice (funds are often pulled before the
// dispute is fully adjudicated).
func (s *Service) LinkChargebackTx(ctx context.Context, id, chargebackTxID uuid.UUID) error {
	rows, err := s.repo.LinkChargebackTx(ctx, id, chargebackTxID)
	if err != nil {
		return err
	}
	if rows == 0 {
		if _, getErr := s.repo.GetDispute(ctx, id); getErr != nil {
			return getErr
		}
		return fmt.Errorf("%w: dispute %s already has a linked chargeback transaction", apperror.ErrChargebackDisputeAlreadyResolved, id)
	}
	return nil
}

// diagnoseNoRows turns a 0-rows-affected status-guarded UPDATE into either
// ErrChargebackDisputeNotFound (the row never existed) or
// ErrChargebackDisputeAlreadyResolved (it existed but wasn't in the status
// this action required — whether that's a genuine race or the case was
// simply already past this point) — same "any status mismatch on a
// terminal-guarded UPDATE is Conflict-shaped" posture as
// ErrScheduledTransactionAlreadyTerminal elsewhere in this package.
func (s *Service) diagnoseNoRows(ctx context.Context, id uuid.UUID) error {
	d, err := s.repo.GetDispute(ctx, id)
	if err != nil {
		return err
	}
	return fmt.Errorf("%w: dispute %s is in status %q", apperror.ErrChargebackDisputeAlreadyResolved, id, d.Status)
}
