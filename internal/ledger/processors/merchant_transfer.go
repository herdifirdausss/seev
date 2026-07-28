package processors

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	"github.com/herdifirdausss/seev/internal/ledger/apperror"
	"github.com/herdifirdausss/seev/internal/ledger/constant"
	"github.com/herdifirdausss/seev/internal/ledger/model"
	"github.com/herdifirdausss/seev/internal/ledger/repository"
	"github.com/herdifirdausss/seev/pkg/generalutil"
)

// =============================================================================
// 30. MerchantTransfer — merchant.cash → destination (as supplied)
//
// Plan 57 T5 §3.3: "source = tenant account, destination as supplied" — the
// SOURCE is always the caller's OWN tenant cash account, resolved
// server-side from Command.MerchantTenantID; it can never be substituted
// by the caller because there is no source-account-id input anywhere on
// this path. Metadata:
//
//	"destination_account_id" (required) — any existing ledger account,
//	    resolved the same way EscrowRelease already resolves a raw account
//	    id from Metadata (this package's own established precedent).
//
// Currency mismatch between source and destination is caught generically
// by service/handle's validateAccounts (every account in AccountIDs must
// share cmd.Currency) BEFORE any ledger_entries are inserted — no
// processor-local currency check is needed here.
//
// Internal-router-only — never added to publicUserTypes
// (internal/ledger/transport/http.go). Reachable only via Gateway's B2B
// path through the internal Post RPC, same posture as FxIn/FxOut/
// Disbursement/InterestAccrue.
//
// [0] = merchant.cash (source, tenant-resolved)
// [1] = destination (as supplied)
// =============================================================================

type MerchantTransfer struct{ repo repository.AccountRepository }

func NewMerchantTransfer(r repository.AccountRepository) *MerchantTransfer {
	return &MerchantTransfer{repo: r}
}
func (p *MerchantTransfer) Type() string { return "merchant_transfer" }

func (p *MerchantTransfer) ResolveAccounts(ctx context.Context, cmd Command) (ResolvedAccounts, string, error) {
	if cmd.MerchantTenantID == uuid.Nil {
		return ResolvedAccounts{}, "", fmt.Errorf("merchant_transfer: MerchantTenantID required")
	}
	destinationID, err := generalutil.MetaUUID(cmd.Metadata, "destination_account_id")
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("merchant_transfer: %w", err)
	}
	sourceID, err := p.repo.GetMerchantAccountID(ctx, cmd.MerchantTenantID, constant.AccountTypeCash)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("merchant_transfer: source cash: %w", err)
	}
	if sourceID == destinationID {
		return ResolvedAccounts{}, "", apperror.NewBizErr(apperror.ErrSelfTransfer, "cannot transfer to own account")
	}
	currency, err := p.repo.GetAccountCurrency(ctx, sourceID)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("merchant_transfer: currency: %w", err)
	}
	return twoLeg(sourceID, destinationID), currency, nil
}

func (p *MerchantTransfer) Validate(ctx context.Context, tx *sql.Tx, cmd ResolvedCommand, bal map[uuid.UUID]model.AccountBalance) error {
	v := MultiValidator{
		PositiveAmountValidator{}, IntegralAmountValidator{},
		SufficientFundsValidator{AccountID: cmd.AccountIDs[0]},
		NotSelfTransferValidator{A: cmd.AccountIDs[0], B: cmd.AccountIDs[1]},
	}
	return v.Validate(ctx, tx, cmd, bal)
}

func (p *MerchantTransfer) BuildEntries(_ context.Context, _ *sql.Tx, cmd ResolvedCommand, _ map[uuid.UUID]model.AccountBalance) ([]model.EntryInstruction, error) {
	return []model.EntryInstruction{
		{AccountID: cmd.AccountIDs[0], Direction: constant.Debit, Amount: cmd.Amount},
		{AccountID: cmd.AccountIDs[1], Direction: constant.Credit, Amount: cmd.Amount},
	}, nil
}

func (p *MerchantTransfer) OutboxEvents(cmd ResolvedCommand, txID uuid.UUID, entries []model.EntryInstruction) []model.OutboxEvent {
	return []model.OutboxEvent{newPostedEvent(cmd, txID, entries)}
}
func (p *MerchantTransfer) AfterCommit(_ context.Context, _ Command) error { return nil }

// ValidateCommand requires MerchantTenantID before any DB work — the same
// fail-fast shape as every other processor's pre-DB check.
func (p *MerchantTransfer) ValidateCommand(_ context.Context, cmd Command) error {
	if cmd.MerchantTenantID == uuid.Nil {
		return fmt.Errorf("%w: merchant_transfer requires MerchantTenantID", apperror.ErrValidation)
	}
	return nil
}
