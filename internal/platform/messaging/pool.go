package messaging

// pool.go — confirm-mode AMQP channel pool.
//
// Why pool confirm channels?
//
//	Opening an AMQP channel requires a Channel.Open + Confirm.Select
//	round-trip per call. Under moderate publish rates this dominates latency.
//	Pooling amortises both round-trips across many Publish calls.
//
// Safety invariants:
//
//	1. Channels that experienced any error are DISCARDED, never returned.
//	   A publish failure may leave the channel's delivery-tag sequence out
//	   of sync with the broker; reusing it would cause confirm mis-routing.
//
//	2. On reconnect, drain() closes every idle channel before any new
//	   Publish call can acquire one. Subsequent acquires call newFn which
//	   allocates against the fresh *amqp.Connection.
//
//	3. Pool size is bounded (maxSize) to cap broker-side channel resources.
//	   Excess channels are closed immediately on release.

import (
	"sync"

	amqp "github.com/rabbitmq/amqp091-go"
)

// confirmedChannel bundles a channel in confirm mode with its notification pipe.
type confirmedChannel struct {
	ch       *amqp.Channel
	confirms chan amqp.Confirmation // buffered(1): one confirm per publish
}

// confirmChannelPool is a bounded LIFO free-list of confirm-mode channels.
// LIFO (last-in, first-out) keeps recently-used channels warm and lets idle
// channels time-out naturally at the broker side when load is low.
type confirmChannelPool struct {
	mu      sync.Mutex
	free    []*confirmedChannel
	maxSize int
	newFn   func() (*confirmedChannel, error)
}

func newConfirmChannelPool(
	maxSize int,
	newFn func() (*confirmedChannel, error),
) *confirmChannelPool {
	if maxSize <= 0 {
		maxSize = defaultChannelPoolSize
	}
	return &confirmChannelPool{
		maxSize: maxSize,
		newFn:   newFn,
		free:    make([]*confirmedChannel, 0, maxSize),
	}
}

// acquire returns an idle channel from the pool or creates a new one.
func (p *confirmChannelPool) acquire() (*confirmedChannel, error) {
	p.mu.Lock()
	if n := len(p.free); n > 0 {
		cc := p.free[n-1]
		p.free = p.free[:n-1]
		p.mu.Unlock()
		return cc, nil
	}
	p.mu.Unlock()
	// Allocate outside the lock — channel open is a network operation.
	return p.newFn()
}

// release returns cc to the pool or closes it if the pool is full or an error
// occurred. hadError must be true whenever the publish path encountered any
// failure after the channel was acquired.
func (p *confirmChannelPool) release(cc *confirmedChannel, hadError bool) {
	if hadError {
		_ = cc.ch.Close()
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.free) >= p.maxSize {
		_ = cc.ch.Close()
		return
	}
	p.free = append(p.free, cc)
}

// drain closes and discards every idle channel.
// Must be called after a reconnect replaces the underlying *amqp.Connection.
// Subsequent acquires will create fresh channels against the new connection.
func (p *confirmChannelPool) drain() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, cc := range p.free {
		_ = cc.ch.Close()
	}
	p.free = p.free[:0]
}
