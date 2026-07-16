package processors

//go:generate mockgen -source=processors.go -destination=processors_mock.go -package=processors
import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/herdifirdausss/seev/internal/ledger/apperror"
	"github.com/herdifirdausss/seev/internal/ledger/constant"
	"github.com/herdifirdausss/seev/internal/ledger/events"
	"github.com/herdifirdausss/seev/internal/ledger/model"
	"github.com/herdifirdausss/seev/internal/ledger/repository"
	"github.com/herdifirdausss/seev/pkg/generalutil"
	"github.com/shopspring/decimal"
)

type Command struct {
	IdempotencyKey   string
	IdempotencyScope string
	Type             string
	Amount           decimal.Decimal
	UserID           uuid.UUID
	TargetUserID     uuid.UUID
	PocketCode       string
	ReferenceID      uuid.UUID
	Metadata         map[string]any
	// QuoteID (docs/plan/38 Task T4), when non-empty, tells execTransfer to
	// consume this fee quote atomically instead of resolving a fee at
	// posting time. A typed field, not a Metadata key — Metadata is
	// stripped/rebuilt on the public router (docs/plan/10 Task T3) before a
	// command ever reaches here.
	QuoteID string
}

// ResolvedCommand is exported so external packages can implement TxProcessor.
type ResolvedCommand struct {
	Command
	AccountIDs  []uuid.UUID
	Currency    string
	Source      uuid.UUID // account debited; uuid.Nil if not a single source->destination pair
	Destination uuid.UUID // account credited; uuid.Nil if not a single source->destination pair
}

// ResolvedAccounts is returned by ResolveAccounts (docs/plan/14 Task T1,
// decision K2). Ordered is the account list BuildEntries continues to index
// positionally ([0]=source, [1]=destination, [2..]=fee/extra legs — this
// positional contract is unchanged and still load-bearing). Source and
// Destination make the same information explicit and semantic instead of
// leaving callers to guess it from position: they land in
// ledger_transactions.source_account_id/destination_account_id.
//
// Source/Destination MAY both be uuid.Nil when the processor's movement
// isn't a single source->destination pair (Reversal can touch more than two
// accounts) — the service tolerates Nil (NULL columns) but asserts that a
// non-Nil value is always a member of Ordered, since a Source/Destination
// pointing at an account BuildEntries never touches would be a processor bug.
type ResolvedAccounts struct {
	Ordered     []uuid.UUID
	Source      uuid.UUID
	Destination uuid.UUID
}

// twoLeg builds a ResolvedAccounts for the common case — source debited,
// destination credited, with optional extra accounts (a fee leg) appended
// after them in Ordered. Every processor except Reversal fits this shape.
func twoLeg(source, destination uuid.UUID, extra ...uuid.UUID) ResolvedAccounts {
	ordered := make([]uuid.UUID, 0, 2+len(extra))
	ordered = append(ordered, source, destination)
	ordered = append(ordered, extra...)
	return ResolvedAccounts{Ordered: ordered, Source: source, Destination: destination}
}

// =============================================================================
// Transaction Type Reference  (v6 — international-grade)
// =============================================================================

// ── MONEY MOVEMENT ───────────────────────────────────────────────────────────
//  money_in              settlement[gateway] → user.cash|pocket  [+fee optional]
//  money_out             user.cash|pocket   → settlement[gateway] [+fee optional]

// ── WITHDRAWAL LIFECYCLE ─────────────────────────────────────────────────────
//  withdraw_initiate       user.cash|pocket → user.hold           [+fee optional]
//  withdraw_pending        user.hold        → user.pending
//  withdraw_settle         user.hold        → settlement[gateway]
//  withdraw_cancel         user.hold        → user.cash|pocket
//  withdraw_pending_settle user.pending     → settlement[gateway]
//  withdraw_pending_cancel user.pending     → user.cash

// ── TRANSFERS ────────────────────────────────────────────────────────────────
//  transfer_p2p            sender.cash      → receiver.cash       [+fee optional]
//  transfer_pocket         user.cash       ↔ user.pocket

// ── MERCHANT / PAYMENT ───────────────────────────────────────────────────────
//  refund                  merchant.settle  → user.cash
//  fee_collect             user.cash        → fee[gateway]        (standalone fee only)
//  chargeback              user.cash        → chargeback[card_network]

// ── ESCROW ───────────────────────────────────────────────────────────────────
//  escrow_hold             buyer.cash       → escrow[currency]    [+fee optional]
//  escrow_release          escrow[currency] → merchant.cash       [+fee optional]
//  escrow_refund           escrow[currency] → buyer.cash

// ── COMPLIANCE / FRAUD ───────────────────────────────────────────────────────
//  freeze_initiate         user.cash        → user.frozen
//  freeze_release          user.frozen      → user.cash
//  freeze_confiscate       user.frozen      → system.confiscated

// ── ADMIN / RECONCILIATION ───────────────────────────────────────────────────
//  adjustment_credit       system.adjustment → user.cash
//  adjustment_debit        user.cash        → system.adjustment
//  reversal                swap all entries of an original tx

// =============================================================================
// INLINE FEE DESIGN
// =============================================================================
//
// Processors marked [+fee optional] support an atomic 3rd entry for fees.
// The fee is resolved and written IN THE SAME DB TRANSACTION as the movement.
//
// This is intentional and important:
//   ✗ 2-step (movement tx → separate fee_collect tx): TOCTOU window exists.
//     Money arrives but fee hasn't been collected yet. Fee can be double-collected
//     if the caller retries. Two outbox events for what is one business action.
//   ✓ Inline fee (single tx, 3 entries): fully atomic. Balance is always
//     consistent. One outbox event with full fee breakdown.
//
// To attach a fee to a supporting processor, set in Metadata:
//   "fee_amount"  — decimal string, must be < cmd.Amount and > 0
//   "fee_gateway" — which fee[gateway] account to credit
//                   (may differ from the movement gateway, e.g. money_in via
//                    "bca" but platform fee goes to "platform")
//
// When fee_amount is absent or zero, the processor behaves as a 2-entry tx.
// No other changes needed at the caller — the processor handles both cases.
//
// Fee account IDs are always last in AccountIDs:
//   2-entry: [src, dst]
//   3-entry: [src, dst, fee[gateway]]
//
// Processors that MUST NOT have inline fees:
//   withdraw_cancel, withdraw_pending_cancel — corrective, not commercial
//   freeze_*, confiscate — compliance/legal, must not silently charge
//   reversal, adjustment — admin correction, adding fee would corrupt the reversal
//
// =============================================================================
// SYSTEM ACCOUNT SHARDING STRATEGY
// =============================================================================
//
// Not all system accounts are hot. Each type has a different shard key.
// The shard key is the second argument of GetSystemAccountID(ctx, type,
// qualifier, currency) — docs/plan/18 Task T2 added currency as a THIRD,
// orthogonal dimension: a qualifier like "bca" now names a family of
// accounts, one per currency (settlement[bca][IDR], settlement[bca][USD]),
// never a single shared pool. Every ResolveAccounts implementation MUST
// resolve the user-facing account (and its currency) BEFORE resolving any
// system account, so the currency it passes is never a guess.
//
// ┌──────────────────┬─────────────────────┬────────────────────────────────────┐
// │ Account Type     │ Shard Key           │ Rationale                          │
// ├──────────────────┼─────────────────────┼────────────────────────────────────┤
// │ settlement       │ gateway             │ 1:1 with real bank/wallet account.  │
// │                  │ "bca","mandiri",    │ Correct double-entry model AND      │
// │                  │ "gopay","ovo"       │ eliminates hot-row. Recon is:       │
// │                  │                     │ ledger_balance == bank_statement.   │
// ├──────────────────┼─────────────────────┼────────────────────────────────────┤
// │ fee              │ gateway             │ Finance tracks MDR per channel:     │
// │                  │ "bca","gopay",      │ fee["bca"] == BCA transfer MDR.    │
// │                  │ "platform","stripe" │ fee["gopay"] == GoPay MDR.         │
// │                  │                     │ "platform" for internal/subscription│
// │                  │                     │ fees with no external gateway.      │
// │                  │                     │ Better hot-path distribution than   │
// │                  │                     │ currency: IDR-only platform would   │
// │                  │                     │ still be hot with currency sharding.│
// ├──────────────────┼─────────────────────┼────────────────────────────────────┤
// │ escrow           │ currency            │ Escrow pool is currency-denominated │
// │                  │ "IDR","USD","SGD"   │ by definition. FX isolation is the  │
// │                  │                     │ primary concern, not write volume.  │
// │                  │                     │ Escrow txs lock briefly — acceptable│
// ├──────────────────┼─────────────────────┼────────────────────────────────────┤
// │ chargeback       │ card_network        │ Visa/MC/JCB have separate dispute  │
// │                  │ "visa","mastercard" │ windows, reason codes, and reports. │
// │                  │                     │ One account per network = clean     │
// │                  │                     │ reconciliation against network data.│
// ├──────────────────┼─────────────────────┼────────────────────────────────────┤
// │ confiscated      │ (none / "")         │ Very low volume. Legal/ops-driven.  │
// │ adjustment       │ (none / "")         │ Not a performance concern.          │
// └──────────────────┴─────────────────────┴────────────────────────────────────┘
//
// WHY fee by gateway instead of currency?
//   Currency sharding is still hot if 95% of volume is IDR via one gateway.
//   Gateway sharding distributes load proportionally to actual traffic per rail,
//   AND maps to how finance actually reports MDR (per payment channel, not per currency).
//   For platforms with a single currency, gateway is always the better key.
//
// WHY NOT shard by date/bucket?
//   Breaks double-entry integrity: counterparty account would change every day,
//   making point-in-time balance queries incorrect. Gateway is stable.
//
// WHY NOT composite key (gateway+currency)?
//   Valid if a single gateway holds multiple currencies (e.g. Stripe multi-currency).
//   Use qualifier = "stripe:USD", "stripe:IDR" only if you need it.
//   Most Indonesian payment rails are single-currency — keep it simple.
//
// NOTE: Rate limits and daily limits → API/policy layer, not here.
// NOTE: FX conversion → orchestration layer (money_out + money_in via FX svc).

// =============================================================================
// Inline fee helpers
// =============================================================================

// resolveInlineFee reads fee metadata and resolves the fee system account.
//
// Returns (uuid.Nil, zero, nil) when "fee_amount" key is absent — caller treats
// this as "no fee configured".
//
// Hard errors (not silently ignored):
//   - "fee_amount" key present but value is not a valid decimal
//   - fee_amount is negative
//   - fee_amount > 0 but "fee_gateway" is absent or empty
//   - fee system account lookup fails
//
// The fee < cmd.Amount constraint is validated by FeeAmountValidator in Validate().
func resolveInlineFee(ctx context.Context, repo repository.AccountRepository, cmd Command, currency string) (feeID uuid.UUID, fee decimal.Decimal, err error) {
	raw, exists := cmd.Metadata["fee_amount"]
	if !exists || raw == nil {
		return uuid.Nil, decimal.Zero, nil
	}
	fee, err = generalutil.MetaDecimal(cmd.Metadata, "fee_amount")
	if err != nil {
		return uuid.Nil, decimal.Zero, fmt.Errorf("%w: fee_amount is not a valid decimal: %v", apperror.ErrValidation, raw)
	}
	if fee.IsZero() {
		return uuid.Nil, decimal.Zero, nil // explicit zero == no fee
	}
	if fee.IsNegative() {
		return uuid.Nil, decimal.Zero, fmt.Errorf("%w: fee_amount must be positive, got %s", apperror.ErrValidation, fee)
	}
	feeGateway, gErr := generalutil.MetaString(cmd.Metadata, "fee_gateway")
	if gErr != nil || feeGateway == "" {
		return uuid.Nil, decimal.Zero, fmt.Errorf("%w: fee_gateway is required when fee_amount is set", apperror.ErrValidation)
	}
	feeID, err = repo.GetSystemAccountID(ctx, constant.AccountTypeFee, feeGateway, currency)
	if err != nil {
		return uuid.Nil, decimal.Zero, fmt.Errorf("inline fee: fee[%s]: %w", feeGateway, err)
	}
	return feeID, fee, nil
}

// requireGateway extracts and validates the "gateway" metadata key.
// Returns ErrValidation if the key is missing or empty.
func requireGateway(cmd Command, processor string) (string, error) {
	gw, err := generalutil.MetaString(cmd.Metadata, "gateway")
	if err != nil || gw == "" {
		return "", fmt.Errorf("%w: %s requires metadata 'gateway' (e.g. 'bca', 'gopay')", apperror.ErrValidation, processor)
	}
	return gw, nil
}

// suspenseQualifier maps a gateway to its suspense system account's
// system_qualifier — "suspense:<gateway>" per the seed data in
// migrations/000008_recon.up.sql (docs/plan/16 Task T2, decision K5). Kept
// distinct from the bare-gateway qualifier settlement/fee accounts use
// (e.g. "bca") since AccountTypeSuspense is already a distinct `type`, but
// the plan document specifies the prefixed qualifier explicitly.
func suspenseQualifier(gateway string) string { return "suspense:" + gateway }

// hasFee reports whether a resolved command carries an inline fee.
// Fee account is always the last element and is only present when len >= 3
// AND "fee_amount" metadata resolves to a positive decimal.
// Safe to call with nil Metadata.
func hasFee(cmd ResolvedCommand) (feeID uuid.UUID, fee decimal.Decimal, ok bool) {
	if len(cmd.AccountIDs) < 3 || len(cmd.Metadata) == 0 {
		return uuid.Nil, decimal.Zero, false
	}
	raw, exists := cmd.Metadata["fee_amount"]
	if !exists || raw == nil {
		return uuid.Nil, decimal.Zero, false
	}
	fee, err := generalutil.MetaDecimal(cmd.Metadata, "fee_amount")
	if err != nil || fee.IsZero() || fee.IsNegative() {
		return uuid.Nil, decimal.Zero, false
	}
	return cmd.AccountIDs[len(cmd.AccountIDs)-1], fee, true
}

// validateOriginalForClose is the shared pre-check used by every lifecycle
// processor that closes a prior transaction (withdraw_settle, withdraw_cancel,
// withdraw_pending_settle, withdraw_pending_cancel, escrow_release,
// escrow_refund — docs/plan/14 Task T2). It reads the original's header
// within the SAME DB transaction and checks: type matches the required
// predecessor, not already closed, status is 'posted', and the amount
// matches exactly (full-amount only — no partial settle in MVP).
//
// This is a fast-fail convenience check against an unlocked-for-write read;
// it is NOT the race-proof guard. The actual guard is the caller (service
// layer) running TransactionRepository.CloseOriginal — a single conditional
// UPDATE — after Validate succeeds. Two concurrent requests can both pass
// this check (both see "not yet closed") and only one will win the
// CloseOriginal race; this function's job is just to produce a clear,
// specific error for the common (non-racing) case instead of a generic one.
func validateOriginalForClose(
	ctx context.Context,
	tx *sql.Tx,
	txRepo repository.TransactionRepository,
	referenceID uuid.UUID,
	requiredOriginalType string,
	amount decimal.Decimal,
) error {
	origType, status, origAmount, closedBy, err := txRepo.GetHeader(ctx, tx, referenceID)
	if err != nil {
		return fmt.Errorf("validate original: %w", err)
	}
	if origType != requiredOriginalType {
		return apperror.NewBizErr(apperror.ErrOriginalTypeMismatch,
			fmt.Sprintf("expected original transaction of type %q, got %q", requiredOriginalType, origType))
	}
	if closedBy != nil {
		return apperror.NewBizErr(apperror.ErrAlreadyClosed,
			fmt.Sprintf("transaction %s was already closed", referenceID))
	}
	if status != "posted" {
		return apperror.NewBizErr(apperror.ErrNotReversible,
			fmt.Sprintf("original status is %q, must be 'posted'", status))
	}
	if !origAmount.Equal(amount) {
		return apperror.NewBizErr(apperror.ErrLifecycleAmountMismatch,
			fmt.Sprintf("amount %s does not match original amount %s (partial settle/cancel/release/refund not supported)", amount, origAmount))
	}
	return nil
}

// requireReferenceID is the shared ValidateCommand pre-DB check for every
// lifecycle processor that closes a prior transaction (docs/plan/14 Task T2)
// — failing fast before any DB work if the caller forgot ReferenceID.
func requireReferenceID(cmd Command, processorType string) error {
	if cmd.ReferenceID == uuid.Nil {
		return fmt.Errorf("%w: %s requires reference_id (the original transaction)", apperror.ErrValidation, processorType)
	}
	return nil
}

// buildEntrySummaries converts BuildEntries' output into the wire-format
// entry list events.TransactionPosted carries (docs/plan/14 Task T3).
func buildEntrySummaries(entries []model.EntryInstruction) []events.EntrySummary {
	out := make([]events.EntrySummary, len(entries))
	for i, e := range entries {
		out[i] = events.EntrySummary{AccountID: e.AccountID, Direction: string(e.Direction), Amount: e.Amount.String()}
	}
	return out
}

// newPostedEvent builds the single ledger.transaction.posted.v1 outbox event
// every processor except Reversal emits (docs/plan/14 Task T3, decision K4).
// Source/Destination come from cmd.Source/cmd.Destination — uuid.Nil becomes
// a nil pointer (omitted from the JSON payload), matching the processors
// whose movement isn't a single source->destination pair (docs/plan/14 Task
// T1, decision K2).
func newPostedEvent(cmd ResolvedCommand, txID uuid.UUID, entries []model.EntryInstruction) model.OutboxEvent {
	var source, dest *uuid.UUID
	if cmd.Source != uuid.Nil {
		s := cmd.Source
		source = &s
	}
	if cmd.Destination != uuid.Nil {
		d := cmd.Destination
		dest = &d
	}
	externalRef, _ := generalutil.MetaString(cmd.Metadata, "external_ref")
	requestID, _ := generalutil.MetaString(cmd.Metadata, "request_id")
	var userID, targetUserID *uuid.UUID
	if cmd.UserID != uuid.Nil {
		u := cmd.UserID
		userID = &u
	}
	if cmd.TargetUserID != uuid.Nil {
		u := cmd.TargetUserID
		targetUserID = &u
	}
	ev := events.NewTransactionPosted(
		txID, cmd.Type, cmd.Amount.String(), cmd.Currency, source, dest,
		buildEntrySummaries(entries), externalRef, time.Now().UTC(),
		userID, targetUserID, requestID,
	)
	return model.OutboxEvent{
		AggregateType: "ledger_transaction", AggregateID: txID,
		EventType: events.TypeTransactionPosted, Payload: ev.ToPayload(),
	}
}

// =============================================================================
// ProcessorRegistry
// =============================================================================

// TxProcessor is the extension point for every transaction type.
//
// [FIX #1 iter3] BuildEntries receives (ctx, *sql.Tx) so processors like Reversal
// can query the DB within the same transaction, avoiding TOCTOU races and the
// value-copy bug where Metadata modifications in ResolveAccounts were lost.
type TxProcessor interface {
	Type() string
	ResolveAccounts(ctx context.Context, cmd Command) (accounts ResolvedAccounts, currency string, err error)
	Validate(ctx context.Context, tx *sql.Tx, cmd ResolvedCommand, balances map[uuid.UUID]model.AccountBalance) error
	BuildEntries(ctx context.Context, tx *sql.Tx, cmd ResolvedCommand, balances map[uuid.UUID]model.AccountBalance) ([]model.EntryInstruction, error)
	OutboxEvents(cmd ResolvedCommand, txID uuid.UUID, entries []model.EntryInstruction) []model.OutboxEvent
	AfterCommit(ctx context.Context, cmd Command) error
	ValidateCommand(ctx context.Context, cmd Command) error // optional pre-DB validation, e.g. check required metadata keys
}

// ProcessorRegistry is read-only after construction — safe for concurrent use.
type ProcessorRegistry struct {
	processors map[string]TxProcessor
}

// NewRegistry panics on duplicate types — fail fast at startup.
func NewRegistry(procs ...TxProcessor) *ProcessorRegistry {
	m := make(map[string]TxProcessor, len(procs))
	for _, p := range procs {
		t := p.Type()
		if _, exists := m[t]; exists {
			panic(fmt.Sprintf("ledger: duplicate processor type %q", t))
		}
		m[t] = p
	}
	return &ProcessorRegistry{processors: m}
}

func (r *ProcessorRegistry) Get(txType string) (TxProcessor, error) {
	p, ok := r.processors[txType]
	if !ok {
		return nil, fmt.Errorf("%w: %q", apperror.ErrUnknownProcessor, txType)
	}
	return p, nil
}

// =============================================================================
// NewDefaultRegistry — wire all 28 processors
//
// Account types required (add to your AccountType constants):
//   AccountTypeCash        — user spendable balance
//   AccountTypeHold        — locked during withdraw initiation
//   AccountTypePending     — escalated: withdrawal unresolved past SLA
//   AccountTypeFrozen      — locked by compliance/fraud investigation
//   AccountTypePocket      — named sub-wallet
//   AccountTypeSettlement  — system settlement (money in/out gateway)
//   AccountTypeEscrow      — marketplace escrow
//   AccountTypeFee         — platform fee collection
//   AccountTypeChargeback  — card network disputes staging
//   AccountTypeConfiscated — permanent seizure (fraud confirmed)
//   AccountTypeAdjustment  — ops/finance manual adjustments source/sink
//
// Limits (per-tx, daily, velocity) belong in your API/policy layer, NOT here.
// FX conversion is an orchestration concern (money_out + money_in via FX service).
// =============================================================================

func NewDefaultRegistry(
	accRepo repository.AccountRepository,
	txRepo repository.TransactionRepository,
) *ProcessorRegistry {
	return NewRegistry(
		// Money movement
		NewMoneyIn(accRepo),
		NewMoneyOut(accRepo),
		// Withdrawal lifecycle
		NewWithdrawInitiate(accRepo),
		NewWithdrawPending(accRepo),
		NewWithdrawSettle(accRepo, txRepo),
		NewWithdrawCancel(accRepo, txRepo),
		NewWithdrawPendingSettle(accRepo, txRepo),
		NewWithdrawPendingCancel(accRepo, txRepo),
		// Transfers
		NewTransferP2P(accRepo),
		NewTransferPocket(accRepo),
		// Merchant / payment
		NewRefund(accRepo),
		NewFeeCollect(accRepo),
		NewChargeback(accRepo),
		// Escrow
		NewEscrowHold(accRepo),
		NewEscrowRelease(accRepo, txRepo),
		NewEscrowRefund(accRepo, txRepo),
		// Compliance / freeze
		NewFreezeInitiate(accRepo),
		NewFreezeRelease(accRepo),
		NewFreezeConfiscate(accRepo),
		// Admin / reconciliation
		NewAdjustmentCredit(accRepo),
		NewAdjustmentDebit(accRepo),
		NewAdjustmentSuspenseCredit(accRepo),
		NewAdjustmentSuspenseDebit(accRepo),
		// FX orchestration primitives (docs/plan/18 Task T3) — internal
		// router only, never added to publicUserTypes.
		NewFxOut(accRepo),
		NewFxIn(accRepo),
		// Batch disbursement (docs/plan/19 Task T2) — internal router only.
		NewDisbursement(accRepo),
		// Interest accrual (docs/plan/19 Task T3) — internal router only.
		NewInterestAccrue(accRepo),
		// Correction
		NewReversal(txRepo, accRepo),
	)
}
