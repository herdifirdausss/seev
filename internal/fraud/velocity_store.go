package fraud

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// VelocityStore atomically deduplicates a posted event and increments its
// user's hourly counter. It also supplies the read side used by the rule.
type VelocityStore interface {
	Get(context.Context, string) (int64, error)
	Record(ctx context.Context, eventID, counterKey string, ttl time.Duration) error
}

type RedisVelocityStore struct{ client *redis.Client }

func NewRedisVelocityStore(client *redis.Client) *RedisVelocityStore {
	return &RedisVelocityStore{client: client}
}

func (s *RedisVelocityStore) Get(ctx context.Context, key string) (int64, error) {
	value, err := s.client.Get(ctx, key).Int64()
	if err == redis.Nil {
		return 0, nil
	}
	return value, err
}

var recordVelocityScript = redis.NewScript(`
if redis.call('SET', KEYS[1], '1', 'EX', ARGV[1], 'NX') then
  local count = redis.call('INCR', KEYS[2])
  if count == 1 then redis.call('EXPIRE', KEYS[2], ARGV[1]) end
  return 1
end
return 0`)

func (s *RedisVelocityStore) Record(ctx context.Context, eventID, counterKey string, ttl time.Duration) error {
	dedupKey := "fraud:velocity:event:" + eventID
	if _, err := recordVelocityScript.Run(ctx, s.client, []string{dedupKey, counterKey}, int64(ttl.Seconds())).Int64(); err != nil {
		return fmt.Errorf("record velocity: %w", err)
	}
	return nil
}
