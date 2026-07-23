package model

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// ReportDailyPosition is one row of v_report_daily_position (docs/roadmap/archive/20
// Task T2) — fund position for one day/currency/account type/owner type.
type ReportDailyPosition struct {
	AsOfDate     time.Time
	Currency     string
	AccountType  string
	OwnerType    string
	AccountCount int
	TotalBalance decimal.Decimal
}

// ReportDailyMutation is one row of v_report_daily_mutation — posted
// transaction volume for one WIB calendar day/type/currency.
type ReportDailyMutation struct {
	ReportDate  time.Time
	TxType      string
	Currency    string
	TxCount     int
	TotalAmount decimal.Decimal
}

// ReportReconSummary is one row of v_report_recon_summary — one
// reconciliation batch's match-status breakdown.
type ReportReconSummary struct {
	BatchID              uuid.UUID
	Gateway              string
	ReportDate           time.Time
	SourceFilename       string
	BatchStatus          string
	DeclaredRowCount     int
	ItemCount            int
	MatchedCount         int
	MissingInternalCount int
	MissingExternalCount int
	AmountMismatchCount  int
	ResolvedCount        int
}
