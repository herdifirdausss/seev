-- ARGV[1] = rate (tokens per millisecond, float)
-- ARGV[2] = capacity (float)
-- ARGV[3] = now (unix millis)
-- ARGV[4] = requested tokens

local rate = tonumber(ARGV[1])
local capacity = tonumber(ARGV[2])
local now = tonumber(ARGV[3])
local requested = tonumber(ARGV[4])

local data = redis.call("HMGET", KEYS[1], "tokens", "last")
local tokens = tonumber(data[1])
local last = tonumber(data[2])

if tokens == nil then
	tokens = capacity
	last = now
end

local delta = math.max(0, now - last)
local refill = delta * rate
tokens = math.min(capacity, tokens + refill)

local allowed = tokens >= requested
if allowed then
	tokens = tokens - requested
end

redis.call("HMSET", KEYS[1],
	"tokens", tokens,
	"last", now
)

local ttl = math.ceil((capacity / rate) * 2)
redis.call("PEXPIRE", KEYS[1], ttl)

return { allowed and 1 or 0, tokens }
