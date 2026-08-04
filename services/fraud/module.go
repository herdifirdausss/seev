// Package fraud is the stable public facade for the Fraud bounded context.
// Business decisions live in internal/fraud; rules, persistence, RPC, and workers
// stay in dedicated packages.
package fraud

import (
	"log/slog"

	"github.com/redis/go-redis/v9"

	"github.com/herdifirdausss/seev/internal/platform/database"
	fraudinternal "github.com/herdifirdausss/seev/services/fraud/internal/fraud"
)

var (
	ErrInvalidScreenInput = fraudinternal.ErrInvalidScreenInput
	ErrInvalidRuleMode    = fraudinternal.ErrInvalidRuleMode
)

type (
	Broker                  = fraudinternal.Broker
	Config                  = fraudinternal.Config
	FailClosedVelocityStore = fraudinternal.FailClosedVelocityStore
	Module                  = fraudinternal.Module
	RedisVelocityStore      = fraudinternal.RedisVelocityStore
	ScreenInput             = fraudinternal.ScreenInput
	ScreeningEvent          = fraudinternal.ScreeningEvent
	VelocityStore           = fraudinternal.VelocityStore
	Verdict                 = fraudinternal.Verdict
)

func NewModule(db database.DatabaseSQL, store VelocityStore, broker Broker, cfg Config, logger *slog.Logger) *Module {
	return fraudinternal.NewModule(db, store, broker, cfg, logger)
}

func NewFailClosedVelocityStore(client *redis.Client, logger *slog.Logger) *FailClosedVelocityStore {
	return fraudinternal.NewFailClosedVelocityStore(client, logger)
}

func NewRedisVelocityStore(client *redis.Client) *RedisVelocityStore {
	return fraudinternal.NewRedisVelocityStore(client)
}
