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
// 10. TransferPocket — user.cash ↔ user.pocket
// Metadata: "direction" = "to_pocket" | "from_pocket"
// =============================================================================

type TransferPocket struct{ repo repository.AccountRepository }

func NewTransferPocket(r repository.AccountRepository) *TransferPocket {
	return &TransferPocket{repo: r}
}
func (p *TransferPocket) Type() string { return "transfer_pocket" }

func (p *TransferPocket) ResolveAccounts(ctx context.Context, cmd Command) (ResolvedAccounts, string, error) {
	if cmd.PocketCode == "" {
		return ResolvedAccounts{}, "", fmt.Errorf("transfer_pocket: PocketCode required")
	}
	cashID, err := p.repo.GetAccountID(ctx, cmd.UserID, constant.AccountTypeCash)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("transfer_pocket: cash: %w", err)
	}
	pocketID, err := p.repo.GetPocketAccountID(ctx, cmd.UserID, cmd.PocketCode)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("transfer_pocket: pocket %q: %w", cmd.PocketCode, err)
	}
	currency, err := p.repo.GetAccountCurrency(ctx, cashID)
	if err != nil {
		return ResolvedAccounts{}, "", fmt.Errorf("transfer_pocket: currency: %w", err)
	}
	return twoLeg(cashID, pocketID), currency, nil
}

func (p *TransferPocket) Validate(ctx context.Context, tx *sql.Tx, cmd ResolvedCommand, bal map[uuid.UUID]model.AccountBalance) error {
	dir, err := generalutil.MetaString(cmd.Metadata, "direction")
	if err != nil || (dir != "to_pocket" && dir != "from_pocket") {
		return fmt.Errorf("%w: metadata 'direction' must be 'to_pocket' or 'from_pocket'", apperror.ErrValidation)
	}
	sourceID := cmd.AccountIDs[0]
	if dir == "from_pocket" {
		sourceID = cmd.AccountIDs[1]
	}
	return MultiValidator{
		PositiveAmountValidator{}, IntegralAmountValidator{},
		SufficientFundsValidator{AccountID: sourceID},
	}.Validate(ctx, tx, cmd, bal)
}

func (p *TransferPocket) BuildEntries(_ context.Context, _ *sql.Tx, cmd ResolvedCommand, _ map[uuid.UUID]model.AccountBalance) ([]model.EntryInstruction, error) {
	dir, _ := generalutil.MetaString(cmd.Metadata, "direction")
	cashID, pocketID := cmd.AccountIDs[0], cmd.AccountIDs[1]
	if dir == "to_pocket" {
		return []model.EntryInstruction{
			{AccountID: cashID, Direction: constant.Debit, Amount: cmd.Amount, Note: "to pocket " + cmd.PocketCode},
			{AccountID: pocketID, Direction: constant.Credit, Amount: cmd.Amount},
		}, nil
	}
	return []model.EntryInstruction{
		{AccountID: pocketID, Direction: constant.Debit, Amount: cmd.Amount, Note: "from pocket " + cmd.PocketCode},
		{AccountID: cashID, Direction: constant.Credit, Amount: cmd.Amount},
	}, nil
}

func (p *TransferPocket) OutboxEvents(cmd ResolvedCommand, txID uuid.UUID, entries []model.EntryInstruction) []model.OutboxEvent {
	return []model.OutboxEvent{newPostedEvent(cmd, txID, entries)}
}
func (p *TransferPocket) AfterCommit(_ context.Context, _ Command) error { return nil }

// ValidateCommand performs pre-DB validation (e.g. required metadata keys).
// No processor-specific requirements beyond what ResolveAccounts/Validate already check.
func (p *TransferPocket) ValidateCommand(_ context.Context, _ Command) error { return nil }
