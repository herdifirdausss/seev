package feepolicy

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/herdifirdausss/seev/internal/ledger/repository"
)

// CachingFeeRepository is a B3 test-double (docs/performance/reports/
// 2026-xx-baseline.md §22): a TTL-memoized decorator over ResolveRule, the
// one repeated-key "routing/resolver" lookup B3 targets. Policy's own doc
// comment states the deliberate default: "there is deliberately no
// process-local cache: admin changes take effect on the next request" — this
// type exists ONLY to measure whether caching helps, gated behind an
// experiment-only env var (ledger.go's NewModule) that defaults to off and
// is never touched by normal product configuration. Every other
// FeeRepository method passes through unchanged via interface embedding.
//
// TTL-only, no invalidation-on-write: acceptable for measuring throughput/
// latency (the experiment's purpose), NOT a complete design — §22's own
// "If activated" consequence list ("define key, value, TTL, invalidation")
// is exactly what would still need building before this could ever ship.
// Hit/miss counts are deliberately NOT tracked here — pg_stat_statements'
// own per-queryid call count (cmd/loadprobe) already gives a cleaner,
// externally-verifiable signal for "how many times did ResolveRule's SELECT
// actually reach Postgres," which is exactly B3's own "repeated-key
// cacheability" evidence requirement; an in-process counter would be
// redundant and one more thing that could disagree with what the database
// itself observed.
type CachingFeeRepository struct {
	repository.FeeRepository
	ttl time.Duration

	mu    sync.RWMutex
	cache map[string]cachedRule
}

type cachedRule struct {
	flatMinorUnits, percentBasisPts int64
	feeGateway                      string
	err                             error
	expiresAt                       time.Time
}

func NewCachingFeeRepository(repo repository.FeeRepository, ttl time.Duration) *CachingFeeRepository {
	return &CachingFeeRepository{FeeRepository: repo, ttl: ttl, cache: make(map[string]cachedRule)}
}

// cacheKey's NUL-delimited join mirrors pkg/cryptox.AAD.bytes()'s own
// rationale — none of these fields can contain NUL, so no ambiguous-join
// bug is possible.
func cacheKey(txType, currency string, userID uuid.UUID, gateway string) string {
	return txType + "\x00" + currency + "\x00" + userID.String() + "\x00" + gateway
}

func (c *CachingFeeRepository) ResolveRule(ctx context.Context, txType, currency string, userID uuid.UUID, gateway string) (flatMinorUnits, percentBasisPts int64, feeGateway string, err error) {
	key := cacheKey(txType, currency, userID, gateway)
	c.mu.RLock()
	entry, ok := c.cache[key]
	c.mu.RUnlock()
	if ok && time.Now().Before(entry.expiresAt) {
		return entry.flatMinorUnits, entry.percentBasisPts, entry.feeGateway, entry.err
	}

	flat, bps, fg, err := c.FeeRepository.ResolveRule(ctx, txType, currency, userID, gateway)
	// Only a genuine "no rule matched" result is cacheable alongside success —
	// a transient infrastructure error must never be memoized (that would
	// turn one bad connection into a sustained false "no fee" outcome for
	// the whole TTL window).
	if err == nil || errors.Is(err, sql.ErrNoRows) {
		c.mu.Lock()
		c.cache[key] = cachedRule{flatMinorUnits: flat, percentBasisPts: bps, feeGateway: fg, err: err, expiresAt: time.Now().Add(c.ttl)}
		c.mu.Unlock()
	}
	return flat, bps, fg, err
}

// GetQuoteWithGateway preserves the optional C5 quote-read capability through
// the experiment-only decorator. Quote reads are never cached here.
func (c *CachingFeeRepository) GetQuoteWithGateway(ctx context.Context, quoteID, userID uuid.UUID) (amount, feeAmount decimal.Decimal, feeGateway, gateway string, err error) {
	reader, ok := c.FeeRepository.(interface {
		GetQuoteWithGateway(context.Context, uuid.UUID, uuid.UUID) (decimal.Decimal, decimal.Decimal, string, string, error)
	})
	if ok {
		return reader.GetQuoteWithGateway(ctx, quoteID, userID)
	}
	amount, feeAmount, feeGateway, err = c.FeeRepository.GetQuote(ctx, quoteID, userID)
	return amount, feeAmount, feeGateway, "", err
}

// TryConsumeQuoteWithGateway preserves exact provider-route matching through
// the experiment-only decorator. Consumption remains an uncached passthrough.
func (c *CachingFeeRepository) TryConsumeQuoteWithGateway(ctx context.Context, exec repository.QueryRower, quoteID, userID uuid.UUID, txType, gateway, currency string, amount decimal.Decimal, ref string) (fee decimal.Decimal, feeGateway string, err error) {
	consumer, ok := c.FeeRepository.(interface {
		TryConsumeQuoteWithGateway(context.Context, repository.QueryRower, uuid.UUID, uuid.UUID, string, string, string, decimal.Decimal, string) (decimal.Decimal, string, error)
	})
	if ok {
		return consumer.TryConsumeQuoteWithGateway(ctx, exec, quoteID, userID, txType, gateway, currency, amount, ref)
	}
	return c.FeeRepository.TryConsumeQuote(ctx, exec, quoteID, userID, txType, currency, amount, ref)
}

// ValidateConsumedPayinQuote preserves the Ledger settlement-authority seam
// through the experiment-only decorator. It remains an uncached, transaction-
// bound read of the immutable quote snapshot.
func (c *CachingFeeRepository) ValidateConsumedPayinQuote(ctx context.Context, exec repository.QueryRower, quoteID, userID uuid.UUID, txType, gateway, currency string, amount, fee decimal.Decimal, feeGateway, ref string) error {
	validator, ok := c.FeeRepository.(interface {
		ValidateConsumedPayinQuote(context.Context, repository.QueryRower, uuid.UUID, uuid.UUID, string, string, string, decimal.Decimal, decimal.Decimal, string, string) error
	})
	if !ok {
		return sql.ErrNoRows
	}
	return validator.ValidateConsumedPayinQuote(ctx, exec, quoteID, userID, txType, gateway, currency, amount, fee, feeGateway, ref)
}
