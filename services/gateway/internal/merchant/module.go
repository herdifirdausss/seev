// Package merchant is the stable public facade for Gateway's Merchant/B2B
// bounded context. Business decisions live in orchestration/; API, auth, lifecycle,
// quota, persistence, and webhook concerns stay in dedicated packages.
package merchant

import (
	"github.com/herdifirdausss/seev/internal/platform/database"
	"github.com/herdifirdausss/seev/internal/platform/security/crypto"
	orchestration "github.com/herdifirdausss/seev/services/gateway/internal/merchant/orchestration"
)

const (
	DefaultObservabilityRefreshInterval = orchestration.DefaultObservabilityRefreshInterval
	DefaultWebhookRelayInterval         = orchestration.DefaultWebhookRelayInterval
)

type (
	LedgerClient = orchestration.LedgerClient
	Module       = orchestration.Module
)

func NewModule(db database.DatabaseSQL, ring *cryptox.Ring, apiKeyPepper string, ledgerClient LedgerClient) *Module {
	return orchestration.NewModule(db, ring, apiKeyPepper, ledgerClient)
}
