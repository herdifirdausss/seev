package constant

type Direction string

const (
	Debit  Direction = "debit"
	Credit Direction = "credit"
)

const (
	AccountTypeCash            = "cash"
	AccountTypeHold            = "hold"
	AccountTypePending         = "pending"
	AccountTypeFee             = "fee"
	AccountTypeSettlement      = "settlement"
	AccountTypeEscrow          = "escrow"
	AccountTypePocket          = "pocket"
	AccountTypeAdjustment      = "adjustment"
	AccountTypeChargeback      = "chargeback"
	AccountTypeFrozen          = "frozen"
	AccountTypeConfiscated     = "confiscated"
	AccountTypeSuspense        = "suspense"
	AccountTypeFxConversion    = "fx_conversion"
	AccountTypeInterestExpense = "interest_expense"

	AccountStatusActive    = "active"
	AccountStatusSuspended = "suspended"
	AccountStatusClosed    = "closed"
)

// ValidGateways is the allowlist of "gateway" metadata values accepted by
// any processor that reads it (money_in, money_out, withdraw_settle,
// withdraw_pending_settle, escrow_release, fee_collect — see
// processors/processors.go's requireGateway). Must stay in sync with the
// settlement/fee system accounts seeded in
// migrations/000002_seed_system_accounts.up.sql — a gateway not listed here
// has no corresponding system account and every posting attempt using it
// would fail downstream anyway; validating here (docs/plan/10 Task T3)
// just gives a clear 400 instead of a confusing lookup error.
var ValidGateways = map[string]bool{
	"bca":      true,
	"gopay":    true,
	"platform": true,
}
