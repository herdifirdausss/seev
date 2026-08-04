// Package assurance is the stable public facade for cross-service
// reconciliation. Decisions live in internal/assurance; rule evaluation remains in
// rules/ and the HTTP surface is kept separate from the service layer.
package assurance

import (
	"log/slog"
	"net/http"

	"github.com/herdifirdausss/seev/internal/platform/config"
	"github.com/herdifirdausss/seev/internal/platform/database"
	"github.com/herdifirdausss/seev/internal/platform/resilience/alerting"
	assuranceinternal "github.com/herdifirdausss/seev/services/assurance/internal/assurance"
)

type (
	FXReconciliationEvidence = assuranceinternal.FXReconciliationEvidence
	FXReconciliationReader   = assuranceinternal.FXReconciliationReader
	FXReconciliationReport   = assuranceinternal.FXReconciliationReport
	Finding                  = assuranceinternal.Finding
	LedgerReader             = assuranceinternal.LedgerReader
	Module                   = assuranceinternal.Module
	PayinReader              = assuranceinternal.PayinReader
	PayoutReader             = assuranceinternal.PayoutReader
	RunSummary               = assuranceinternal.RunSummary
)

func NewModule(db database.DatabaseSQL, cfg config.AssuranceConfig, payin PayinReader, payout PayoutReader, ledger LedgerReader, alertFn alerting.AlertFunc, logger *slog.Logger, fxReaders ...FXReconciliationReader) *Module {
	return assuranceinternal.NewModule(db, cfg, payin, payout, ledger, alertFn, logger, fxReaders...)
}

func NewFXReconciliationClient(baseURL, internalToken string, httpClient *http.Client) FXReconciliationReader {
	return assuranceinternal.NewFXReconciliationClient(baseURL, internalToken, httpClient)
}
