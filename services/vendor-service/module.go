// Package vendorboundary is the public composition boundary for the Vendor
// service. The implementation remains private to this service; Payin and
// Payout use this facade when they need a service-owned client or adapter.
package vendorboundary

import (
	vendorgw "github.com/herdifirdausss/seev/contracts/vendorgw"
	vendorv1 "github.com/herdifirdausss/seev/gen/go/vendorservice/v1"
	"github.com/herdifirdausss/seev/internal/platform/database"
	"github.com/herdifirdausss/seev/internal/platform/security/crypto"
	internalvendor "github.com/herdifirdausss/seev/services/vendor-service/internal"
	"github.com/redis/go-redis/v9"
	"log/slog"
)

var ErrUnknownVendor = internalvendor.ErrUnknownVendor

type (
	Adapter              = internalvendor.Adapter
	CallbackHandler      = internalvendor.CallbackHandler
	CallbackVerifier     = internalvendor.CallbackVerifier
	Client               = internalvendor.Client
	MockAdapter          = internalvendor.MockAdapter
	NormalizedCallback   = internalvendor.NormalizedCallback
	PayinAvailability    = internalvendor.PayinAvailability
	PayinCallbackClient  = internalvendor.PayinCallbackClient
	PayoutProvider       = internalvendor.PayoutProvider
	PayoutCallbackClient = internalvendor.PayoutCallbackClient
	Registry             = internalvendor.Registry
	Server               = internalvendor.Server
)

func NewClient(rpc vendorv1.VendorServiceClient) *Client {
	return internalvendor.NewClient(rpc)
}

func NewPayinAvailability(name string) vendorgw.PayinVendor {
	return internalvendor.NewPayinAvailability(name)
}

func NewPayoutProvider(name string, rpc vendorv1.VendorServiceClient) *PayoutProvider {
	return internalvendor.NewPayoutProvider(name, rpc)
}

func NewRegistry() *Registry { return internalvendor.NewRegistry() }

func NewMockAdapter(name string, secrets ...string) *MockAdapter {
	return internalvendor.NewMockAdapter(name, secrets...)
}

func NewServer(registry *Registry, db ...*database.DBSQL) *Server {
	return internalvendor.NewServer(registry, db...)
}

func NewCallbackHandler(db *database.DBSQL, ring *cryptox.Ring, registry *Registry, payin PayinCallbackClient, payout PayoutCallbackClient, allowedCIDRs, trustedProxyCIDRs string) (*CallbackHandler, error) {
	return internalvendor.NewCallbackHandler(db, ring, registry, payin, payout, allowedCIDRs, trustedProxyCIDRs)
}

func StartRetentionRunner(db *database.DBSQL, redisClient *redis.Client, logger *slog.Logger) (func(), error) {
	return internalvendor.StartRetentionRunner(db, redisClient, logger)
}
