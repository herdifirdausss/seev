// Package policy implements per-user/per-type transaction limits and
// velocity checks (docs/plan/17 Task T1, decision K-S S1) — evaluated in
// the ledger's HTTP transport layer BEFORE a transaction is posted. The
// ledger module itself never imports this package and has no awareness of
// it (see internal/ledger/processors/processors.go's own comment: "Limits
// ... belong in your API/policy layer, NOT here").
package policy

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/herdifirdausss/seev/pkg/alerting"
	"github.com/herdifirdausss/seev/pkg/cache"
)

// defaultLimitCacheTTL bounds how stale an in-process cached limit row can
// be — a limit change made via the admin endpoint takes effect within this
// window on every process replica, no pub/sub invalidation needed
// (docs/plan/17 Task T1 step 4). Overridable via WithCacheTTL, primarily so
// integration tests can prove cache-expiry behavior without a 60s+ sleep.
const defaultLimitCacheTTL = 60 * time.Second

// defaultAlertThrottle bounds how often a fail-open alert fires (docs/plan/25
// Task T5) — a Redis/DB outage causes EVERY Check call to fail open for as
// long as the outage lasts, so without throttling a busy period would fire
// one alert per request (an alert storm indistinguishable from a DoS on the
// alerting webhook itself), not one alert per incident. Overridable via
// WithAlertThrottle, primarily so tests can observe a second alert without
// a 60s+ sleep.
const defaultAlertThrottle = 60 * time.Second

// Engine evaluates and records policy limits. Construct once at startup,
// safe for concurrent use.
type Engine struct {
	repo     Repository
	counter  cache.Counter
	loc      *time.Location
	logger   *slog.Logger
	cacheTTL time.Duration

	cacheMu sync.RWMutex
	cache   map[string]cachedLimit

	// alertFn fires a fail-open alert (docs/plan/25 Task T5) — nil means
	// no alerting configured (byte-identical to before this feature
	// existed: fail-open still happens, only the log line, no alert).
	alertFn       alerting.AlertFunc
	alertThrottle time.Duration
	// lastAlertNano is the UnixNano of the last alert actually fired,
	// guarded by CompareAndSwap so concurrent Check calls during an
	// outage race to fire at most one alert per alertThrottle window
	// instead of serializing on a mutex for every failed request.
	lastAlertNano atomic.Int64
}

type cachedLimit struct {
	limit    Limit
	found    bool
	expireAt time.Time
}

// Option configures an Engine at construction time.
type Option func(*Engine)

// WithCacheTTL overrides defaultLimitCacheTTL — used by tests that need to
// observe cache expiry quickly rather than waiting out the production
// default.
func WithCacheTTL(d time.Duration) Option {
	return func(e *Engine) { e.cacheTTL = d }
}

// WithAlertFunc wires a fail-open alert sink (docs/plan/25 Task T5) —
// every fail-open branch in Check fires this, severity="warning", throttled
// to defaultAlertThrottle (overridable via WithAlertThrottle). nil (the
// default) disables alerting entirely — fail-open behavior itself is
// unchanged either way, only whether anyone is notified.
func WithAlertFunc(fn alerting.AlertFunc) Option {
	return func(e *Engine) { e.alertFn = fn }
}

// WithAlertThrottle overrides defaultAlertThrottle — used by tests that
// need to observe a second alert fire without waiting out the production
// default.
func WithAlertThrottle(d time.Duration) Option {
	return func(e *Engine) { e.alertThrottle = d }
}

// New constructs a policy Engine. counter selects Redis or in-memory
// (docs/plan/12 Task T1 fallback pattern) at the caller's discretion — this
// package has no opinion on which. loc anchors daily/monthly window
// boundaries (must match the timezone snapshots/statements use — Asia/
// Jakarta in this deployment) so "today" means the same calendar day
// everywhere in the system.
func New(repo Repository, counter cache.Counter, loc *time.Location, logger *slog.Logger, opts ...Option) *Engine {
	if logger == nil {
		logger = slog.Default()
	}
	if loc == nil {
		loc = time.UTC
	}
	e := &Engine{
		repo: repo, counter: counter, loc: loc, logger: logger,
		cacheTTL: defaultLimitCacheTTL, cache: make(map[string]cachedLimit),
		alertThrottle: defaultAlertThrottle,
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// fireFailOpenAlert notifies alertFn (if configured) that Check just failed
// open, throttled to at most one delivery per alertThrottle window —
// delivery itself runs in its own goroutine against a background context
// (never ctx, which is cancelled the moment the HTTP request that
// triggered this fail-open returns) so a slow/unreachable alert webhook
// never adds latency to the transaction path it's reporting a problem
// with.
func (e *Engine) fireFailOpenAlert(reason string) {
	if e.alertFn == nil {
		return
	}
	now := time.Now().UnixNano()
	throttleNanos := e.alertThrottle.Nanoseconds()
	for {
		last := e.lastAlertNano.Load()
		if now-last < throttleNanos {
			return
		}
		if e.lastAlertNano.CompareAndSwap(last, now) {
			break
		}
	}
	alertFn := e.alertFn
	logger := e.logger
	go func() {
		if err := alertFn(context.Background(), "warning", "policy: fail-open — "+reason); err != nil {
			logger.Error("policy: fail-open alert delivery failed", slog.Any("error", err))
		}
	}()
}

// Check evaluates userID's limits for a transaction of the given type and
// amount. allowed=false means the request must be rejected; rule names
// which dimension was violated ("max_per_tx", "max_daily_amount",
// "max_daily_count", "max_monthly_amount") and detail is a human-readable
// message. A transaction type with no configured limit row, or a limit row
// with enabled=false, is unbounded — Check returns allowed=true.
//
// Check FAILS OPEN on infrastructure errors (repository or counter
// unreachable) — logs and allows the request rather than returning an
// error that would 500 every posting of that type. Same convention as this
// codebase's rate limiter (docs/plan/12 Task T1): velocity limits are a
// coarse business control, not a money-safety invariant (that remains the
// ledger's own job) — a Redis/DB blip must not become a denial-of-service
// for legitimate traffic. The returned err is therefore always nil in
// practice; it exists for a future caller that wants to distinguish
// fail-open events, not for the current transport layer to act on.
//
// Check does NOT itself record usage — call Record after the transaction
// actually posts. This means concurrent requests can both pass Check before
// either calls Record (a small race) — accepted deliberately, same
// reasoning as above.
func (e *Engine) Check(ctx context.Context, userID uuid.UUID, txType string, amount decimal.Decimal) (allowed bool, rule string, detail string, err error) {
	limit, found, err := e.getLimit(ctx, userID, txType)
	if err != nil {
		e.logger.Warn("policy: load limit failed, failing open", slog.Any("error", err), slog.String("user_id", userID.String()), slog.String("type", txType))
		e.fireFailOpenAlert("load limit failed: " + err.Error())
		return true, "", "", nil
	}
	if !found || !limit.Enabled {
		return true, "", "", nil
	}

	if limit.MaxPerTx != nil {
		max := decimal.NewFromInt(*limit.MaxPerTx)
		if amount.GreaterThan(max) {
			return false, "max_per_tx", fmt.Sprintf("amount %s exceeds per-transaction limit %s", amount, max), nil
		}
	}

	now := time.Now().In(e.loc)

	if limit.MaxDailyAmount != nil {
		cur, err := e.counter.Get(ctx, dailyAmountKey(userID, txType, now))
		if err != nil {
			e.logger.Warn("policy: read daily amount counter failed, failing open", slog.Any("error", err), slog.String("user_id", userID.String()), slog.String("type", txType))
			e.fireFailOpenAlert("read daily amount counter failed: " + err.Error())
			return true, "", "", nil
		}
		if decimal.NewFromInt(cur).Add(amount).GreaterThan(decimal.NewFromInt(*limit.MaxDailyAmount)) {
			return false, "max_daily_amount", fmt.Sprintf("would exceed daily amount limit %d (used %d so far today)", *limit.MaxDailyAmount, cur), nil
		}
	}
	if limit.MaxDailyCount != nil {
		cur, err := e.counter.Get(ctx, dailyCountKey(userID, txType, now))
		if err != nil {
			e.logger.Warn("policy: read daily count counter failed, failing open", slog.Any("error", err), slog.String("user_id", userID.String()), slog.String("type", txType))
			e.fireFailOpenAlert("read daily count counter failed: " + err.Error())
			return true, "", "", nil
		}
		if cur+1 > int64(*limit.MaxDailyCount) {
			return false, "max_daily_count", fmt.Sprintf("would exceed daily count limit %d (used %d so far today)", *limit.MaxDailyCount, cur), nil
		}
	}
	if limit.MaxMonthlyAmount != nil {
		cur, err := e.counter.Get(ctx, monthlyAmountKey(userID, txType, now))
		if err != nil {
			e.logger.Warn("policy: read monthly amount counter failed, failing open", slog.Any("error", err), slog.String("user_id", userID.String()), slog.String("type", txType))
			e.fireFailOpenAlert("read monthly amount counter failed: " + err.Error())
			return true, "", "", nil
		}
		if decimal.NewFromInt(cur).Add(amount).GreaterThan(decimal.NewFromInt(*limit.MaxMonthlyAmount)) {
			return false, "max_monthly_amount", fmt.Sprintf("would exceed monthly amount limit %d (used %d so far this month)", *limit.MaxMonthlyAmount, cur), nil
		}
	}

	return true, "", "", nil
}

// Record increments the velocity counters for a transaction that just
// posted successfully. Call this AFTER ledger.Post succeeds, never before
// — a posting that fails validation/insufficient-funds/etc. must not
// consume quota. Best-effort: a counter-increment failure is logged, not
// returned, since the money movement itself already happened and cannot be
// undone by a bookkeeping-counter error.
func (e *Engine) Record(ctx context.Context, userID uuid.UUID, txType string, amount decimal.Decimal) {
	now := time.Now().In(e.loc)
	amt := amount.IntPart()

	if _, err := e.counter.IncrBy(ctx, dailyAmountKey(userID, txType, now), amt, 48*time.Hour); err != nil {
		e.logger.Error("policy: record daily amount failed", slog.Any("error", err), slog.String("user_id", userID.String()), slog.String("type", txType))
	}
	if _, err := e.counter.IncrBy(ctx, dailyCountKey(userID, txType, now), 1, 48*time.Hour); err != nil {
		e.logger.Error("policy: record daily count failed", slog.Any("error", err), slog.String("user_id", userID.String()), slog.String("type", txType))
	}
	if _, err := e.counter.IncrBy(ctx, monthlyAmountKey(userID, txType, now), amt, 35*24*time.Hour); err != nil {
		e.logger.Error("policy: record monthly amount failed", slog.Any("error", err), slog.String("user_id", userID.String()), slog.String("type", txType))
	}
}

// getLimit resolves the effective limit for userID+txType, cached
// in-process for e.cacheTTL.
func (e *Engine) getLimit(ctx context.Context, userID uuid.UUID, txType string) (Limit, bool, error) {
	key := userID.String() + ":" + txType

	e.cacheMu.RLock()
	c, ok := e.cache[key]
	e.cacheMu.RUnlock()
	if ok && time.Now().Before(c.expireAt) {
		return c.limit, c.found, nil
	}

	limit, found, err := e.repo.GetEffective(ctx, userID, txType)
	if err != nil {
		return Limit{}, false, err
	}

	e.cacheMu.Lock()
	e.cache[key] = cachedLimit{limit: limit, found: found, expireAt: time.Now().Add(e.cacheTTL)}
	e.cacheMu.Unlock()

	return limit, found, nil
}

func dailyAmountKey(userID uuid.UUID, txType string, t time.Time) string {
	return fmt.Sprintf("pol:%s:%s:d:%s:amt", userID, txType, t.Format("2006-01-02"))
}
func dailyCountKey(userID uuid.UUID, txType string, t time.Time) string {
	return fmt.Sprintf("pol:%s:%s:d:%s:cnt", userID, txType, t.Format("2006-01-02"))
}
func monthlyAmountKey(userID uuid.UUID, txType string, t time.Time) string {
	return fmt.Sprintf("pol:%s:%s:m:%s:amt", userID, txType, t.Format("2006-01"))
}
