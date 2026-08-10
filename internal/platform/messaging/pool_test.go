package messaging

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakePublishChannel struct {
	mu          sync.Mutex
	closed      int
	publishErr  error
	publishings []amqp.Publishing
}

func (f *fakePublishChannel) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed++
	return nil
}

func (f *fakePublishChannel) PublishWithContext(_ context.Context, _, _ string, _, _ bool, publishing amqp.Publishing) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.publishings = append(f.publishings, publishing)
	return f.publishErr
}

func TestConfirmChannelPoolLifecycle(t *testing.T) {
	var created atomic.Int32
	channels := make([]*fakePublishChannel, 0, 3)
	var mu sync.Mutex
	pool := newConfirmChannelPool(1, func() (*confirmedChannel, error) {
		created.Add(1)
		ch := &fakePublishChannel{}
		mu.Lock()
		channels = append(channels, ch)
		mu.Unlock()
		return &confirmedChannel{ch: ch, confirms: make(chan amqp.Confirmation, 1)}, nil
	})

	first, err := pool.acquire()
	require.NoError(t, err)
	pool.release(first, false)
	reused, err := pool.acquire()
	require.NoError(t, err)
	assert.Same(t, first, reused)
	assert.Equal(t, int32(1), created.Load())

	second, err := pool.acquire()
	require.NoError(t, err)
	pool.release(reused, false)
	pool.release(second, false)
	assert.Equal(t, 1, channels[1].closed, "overflow channel must be closed")

	idle, err := pool.acquire()
	require.NoError(t, err)
	pool.release(idle, true)
	assert.Equal(t, 1, channels[0].closed, "errored channel must be closed")

	replacement, err := pool.acquire()
	require.NoError(t, err)
	pool.release(replacement, false)
	pool.drain()
	assert.Equal(t, 1, channels[2].closed)
}

func TestConfirmChannelPoolFactoryError(t *testing.T) {
	want := errors.New("open failed")
	pool := newConfirmChannelPool(0, func() (*confirmedChannel, error) { return nil, want })
	_, err := pool.acquire()
	require.ErrorIs(t, err, want)
}
