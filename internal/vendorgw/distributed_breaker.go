package vendorgw

import (
	"context"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// defaultRedisTimeout bounds every individual Redis round trip a
// DistributedBreaker makes (docs/roadmap/archive/45 K3: "Every Redis operation is
// bounded by a short timeout") — short enough that a stalled Redis never
// meaningfully delays the payout/payin request path before falling back
// to the local tracker.
const defaultRedisTimeout = 150 * time.Millisecond

// allowScript implements the SAME state machine as HealthTracker.Allow,
// atomically: closed -> true; open (cooldown not elapsed) -> false; open
// (cooldown elapsed) -> exactly one caller wins the probe token (SET NX
// PX) and becomes half-open, everyone else sees half-open and is denied;
// half-open with a live (unexpired) probe token -> false; half-open whose
// probe token already expired (the prober crashed or its own call outlived
// probeTTL) -> treated like a fresh cooldown-elapsed open, so the breaker
// can recover a probe slot no live process is actually holding.
//
// KEYS[1] = state hash ("breaker:<ns>:state:<vendor>")
// KEYS[2] = probe token key ("breaker:<ns>:probe:<vendor>")
// ARGV[1] = now (unix ms)
// ARGV[2] = cooldown (ms)
// ARGV[3] = probe ttl (ms)
// ARGV[4] = this caller's probe token value (unique per call)
var allowScript = redis.NewScript(`
local state = redis.call("HGET", KEYS[1], "state")
if state == false or state == "closed" then
	return 1
end

if state == "half_open" then
	if redis.call("EXISTS", KEYS[2]) == 1 then
		return 0
	end
	-- probe token expired: fall through and treat like open+cooldown-elapsed
end

local opened_at = tonumber(redis.call("HGET", KEYS[1], "opened_at")) or 0
local now = tonumber(ARGV[1])
local cooldown = tonumber(ARGV[2])
if state == "open" and (now - opened_at) < cooldown then
	return 0
end

local ok = redis.call("SET", KEYS[2], ARGV[4], "NX", "PX", ARGV[3])
if ok then
	redis.call("HSET", KEYS[1], "state", "half_open")
	return 1
end
return 0
`)

// recordSuccessScript unconditionally closes the circuit and clears the
// probe token — mirrors HealthTracker.RecordSuccess exactly, including
// closing from a fresh vendor (no prior state) as a no-op-shaped write.
//
// KEYS[1] = state hash, KEYS[2] = probe token key
var recordSuccessScript = redis.NewScript(`
redis.call("HSET", KEYS[1], "state", "closed", "consecutive_failures", "0", "opened_at", "0")
redis.call("DEL", KEYS[2])
return 1
`)

// recordFailureScript mirrors HealthTracker.RecordFailure: a half-open
// probe failing re-opens immediately (no re-accumulating the threshold —
// one failed probe already proves the vendor is still down); otherwise
// increments consecutive_failures and opens once the threshold is reached.
//
// KEYS[1] = state hash, KEYS[2] = probe token key
// ARGV[1] = now (unix ms), ARGV[2] = failure threshold
var recordFailureScript = redis.NewScript(`
local state = redis.call("HGET", KEYS[1], "state")
if state == "half_open" then
	redis.call("HSET", KEYS[1], "state", "open", "opened_at", ARGV[1])
	redis.call("DEL", KEYS[2])
	return 1
end

local failures = redis.call("HINCRBY", KEYS[1], "consecutive_failures", 1)
local threshold = tonumber(ARGV[2])
if (state == false or state == "closed") and failures >= threshold then
	redis.call("HSET", KEYS[1], "state", "open", "opened_at", ARGV[1])
end
return 1
`)

// DistributedBreaker is a Redis-backed circuit breaker (docs/roadmap/archive/45 Task
// T2/K3) that converges state across every replica sharing the same Redis
// key namespace, falling back to an embedded per-process HealthTracker
// whenever Redis itself is unreachable or erroring. It implements Breaker,
// so every call site (internal/payin/payout's routing and dispatch code)
// is unaffected by which backend is actually active.
//
// Backend selection is per-CALL, not sticky: every Allow/RecordSuccess/
// RecordFailure independently tries Redis first and falls back to local
// only on that call's own error — there is no "stay degraded" latch, since
// Redis recovering mid-outage should be picked back up on the very next
// call, not wait for some separate health-probe loop. A degrade/recover
// TRANSITION (the backend actually used differs from the previous call's)
// is logged exactly once and reflected in the vendorgw_breaker_backend
// gauge; a steady string of calls on the same backend produces no
// per-call log noise.
//
// State accumulated locally during a Redis outage is NEVER merged back
// into Redis once Redis recovers (docs/roadmap/archive/45 K3) — Redis simply becomes
// authoritative again from that point forward. This can very rarely let a
// vendor that tripped the LOCAL fallback stay briefly available in Redis's
// view (or vice versa) across an outage boundary; this is accepted because
// the breaker is purely an availability optimization (docs/roadmap/archive/40) — the
// anti-double-payout guarantee never depends on breaker state agreeing
// anywhere.
type DistributedBreaker struct {
	redis     *redis.Client
	namespace string
	local     *HealthTracker

	failureThreshold int
	cooldown         time.Duration
	probeTTL         time.Duration
	redisTimeout     time.Duration
	logger           *slog.Logger

	mu          sync.Mutex
	usingRedis  bool // last call's actual backend; drives degrade/recover log+gauge edges only
	initialized bool // false until the first call resolves — suppresses a misleading "recovered" log on startup
}

var _ Breaker = (*DistributedBreaker)(nil)

// NewDistributedBreaker constructs a distributed breaker. namespace scopes
// every Redis key (e.g. "payin", "payout") so two modules sharing one
// Redis instance/DB never collide on a same-named vendor. probeTTL must
// exceed the maximum configured vendor call timeout (docs/roadmap/archive/45 K3) —
// callers are responsible for passing a value that does; this constructor
// only applies a floor default, it cannot know the caller's actual vendor
// timeout. Zero failureThreshold/cooldown/probeTTL/redisTimeout fall back
// to HealthTracker's own defaults (or, for probeTTL/redisTimeout, this
// package's own floor).
func NewDistributedBreaker(client *redis.Client, namespace string, failureThreshold int, cooldown, probeTTL time.Duration, logger *slog.Logger) *DistributedBreaker {
	if logger == nil {
		logger = slog.Default()
	}
	if probeTTL <= 0 {
		probeTTL = 30 * time.Second
	}
	return &DistributedBreaker{
		redis:            client,
		namespace:        namespace,
		local:            NewHealthTracker(failureThreshold, cooldown, logger),
		failureThreshold: failureThreshold,
		cooldown:         cooldown,
		probeTTL:         probeTTL,
		redisTimeout:     defaultRedisTimeout,
		logger:           logger,
	}
}

func (d *DistributedBreaker) stateKey(vendor string) string {
	return "breaker:" + d.namespace + ":state:" + vendor
}

func (d *DistributedBreaker) probeKey(vendor string) string {
	return "breaker:" + d.namespace + ":probe:" + vendor
}

func (d *DistributedBreaker) vendorSetKey() string {
	return "breaker:" + d.namespace + ":vendors"
}

// onBackendResult records which backend THIS call actually used and logs
// once on a degrade (redis->local) or recover (local->redis) transition —
// never on a steady run of calls against the same backend.
func (d *DistributedBreaker) onBackendResult(usedRedis bool, cause error) {
	d.mu.Lock()
	changed := d.initialized && usedRedis != d.usingRedis
	d.usingRedis = usedRedis
	d.initialized = true
	d.mu.Unlock()

	breakerBackend.WithLabelValues(d.namespace, "redis").Set(boolFloat(usedRedis))
	breakerBackend.WithLabelValues(d.namespace, "local").Set(boolFloat(!usedRedis))
	if !changed {
		return
	}
	if usedRedis {
		d.logger.Info("vendorgw: distributed breaker backend recovered to redis", slog.String("namespace", d.namespace))
	} else {
		d.logger.Warn("vendorgw: distributed breaker backend degraded to local", slog.String("namespace", d.namespace), slog.Any("error", cause))
	}
}

func boolFloat(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

func (d *DistributedBreaker) Allow(ctx context.Context, vendor string) bool {
	rctx, cancel := context.WithTimeout(ctx, d.redisTimeout)
	defer cancel()

	token := uuid.NewString()
	res, err := allowScript.Run(rctx, d.redis,
		[]string{d.stateKey(vendor), d.probeKey(vendor)},
		time.Now().UnixMilli(), d.cooldown.Milliseconds(), d.probeTTL.Milliseconds(), token,
	).Int64()
	if err != nil {
		d.onBackendResult(false, err)
		return d.local.Allow(ctx, vendor)
	}
	d.markVendorKnown(ctx, vendor)
	d.onBackendResult(true, nil)
	return res == 1
}

func (d *DistributedBreaker) RecordSuccess(ctx context.Context, vendor string) {
	rctx, cancel := context.WithTimeout(ctx, d.redisTimeout)
	defer cancel()

	err := recordSuccessScript.Run(rctx, d.redis, []string{d.stateKey(vendor), d.probeKey(vendor)}).Err()
	if err != nil {
		d.onBackendResult(false, err)
		d.local.RecordSuccess(ctx, vendor)
		return
	}
	d.markVendorKnown(ctx, vendor)
	d.onBackendResult(true, nil)
}

func (d *DistributedBreaker) RecordFailure(ctx context.Context, vendor string) {
	rctx, cancel := context.WithTimeout(ctx, d.redisTimeout)
	defer cancel()

	err := recordFailureScript.Run(rctx, d.redis,
		[]string{d.stateKey(vendor), d.probeKey(vendor)},
		time.Now().UnixMilli(), d.failureThreshold,
	).Err()
	if err != nil {
		d.onBackendResult(false, err)
		d.local.RecordFailure(ctx, vendor)
		return
	}
	d.markVendorKnown(ctx, vendor)
	d.onBackendResult(true, nil)
}

// markVendorKnown adds vendor to the namespace's vendor set so Snapshot
// can enumerate it — a plain SADD, not part of the atomic Lua scripts
// above (Snapshot is an admin read, not on the money-moving path, so this
// doesn't need to share their atomicity). A failure here is logged and
// swallowed: it can only make a vendor briefly absent from the admin
// Snapshot view, never affect Allow/Record's own correctness.
func (d *DistributedBreaker) markVendorKnown(ctx context.Context, vendor string) {
	rctx, cancel := context.WithTimeout(ctx, d.redisTimeout)
	defer cancel()
	if err := d.redis.SAdd(rctx, d.vendorSetKey(), vendor).Err(); err != nil {
		d.logger.Warn("vendorgw: distributed breaker failed to record vendor for snapshot",
			slog.String("namespace", d.namespace), slog.Any("error", err))
	}
}

// Snapshot merges Redis's view (authoritative for any vendor it knows
// about) with the local tracker's view (vendors only ever seen during a
// Redis outage). Unlike Allow/Record*, a Redis error here degrades to
// returning whatever Redis-known vendors were already read plus the local
// tracker's own snapshot — Snapshot is a best-effort admin read, never a
// money-affecting call, so a partial result beats an error.
func (d *DistributedBreaker) Snapshot(ctx context.Context) []VendorHealth {
	rctx, cancel := context.WithTimeout(ctx, d.redisTimeout)
	defer cancel()

	seen := make(map[string]bool)
	out := make([]VendorHealth, 0)

	vendors, err := d.redis.SMembers(rctx, d.vendorSetKey()).Result()
	if err != nil {
		d.onBackendResult(false, err)
	} else {
		d.onBackendResult(true, nil)
		for _, vendor := range vendors {
			h, ok := d.snapshotOneFromRedis(rctx, vendor)
			if !ok {
				continue
			}
			seen[vendor] = true
			out = append(out, h)
		}
	}

	for _, h := range d.local.Snapshot(ctx) {
		if seen[h.Vendor] {
			continue
		}
		out = append(out, h)
	}
	return out
}

func (d *DistributedBreaker) snapshotOneFromRedis(ctx context.Context, vendor string) (VendorHealth, bool) {
	m, err := d.redis.HGetAll(ctx, d.stateKey(vendor)).Result()
	if err != nil || len(m) == 0 {
		return VendorHealth{}, false
	}
	h := VendorHealth{Vendor: vendor, State: HealthState(m["state"])}
	if h.State == "" {
		h.State = StateClosed
	}
	if v, ok := m["consecutive_failures"]; ok {
		if n, perr := strconv.Atoi(v); perr == nil {
			h.ConsecutiveFailures = n
		}
	}
	if v, ok := m["opened_at"]; ok {
		if ms, perr := strconv.ParseInt(v, 10, 64); perr == nil && ms > 0 {
			h.OpenedAt = time.UnixMilli(ms)
		}
	}
	return h, true
}
