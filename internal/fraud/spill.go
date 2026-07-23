package fraud

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/herdifirdausss/seev/internal/fraud/model"
)

const (
	maxSpillEvents = 1000
	spillFlushTick = time.Second
)

type eventSpill struct {
	mu     sync.Mutex
	events []model.ScreeningEvent
	lost   uint64
	wake   chan struct{}
}

func newEventSpill() *eventSpill {
	return &eventSpill{wake: make(chan struct{}, 1)}
}

func (s *eventSpill) enqueue(event model.ScreeningEvent) {
	s.mu.Lock()
	if len(s.events) >= maxSpillEvents {
		copy(s.events, s.events[1:])
		s.events[len(s.events)-1] = event
		s.lost++
	} else {
		s.events = append(s.events, event)
	}
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *eventSpill) peek() (model.ScreeningEvent, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.events) == 0 {
		return model.ScreeningEvent{}, false
	}
	return s.events[0], true
}

func (s *eventSpill) pop() {
	s.mu.Lock()
	if len(s.events) > 0 {
		s.events[0] = model.ScreeningEvent{}
		s.events = s.events[1:]
	}
	s.mu.Unlock()
}

func (s *eventSpill) depth() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.events)
}

func (s *eventSpill) lostCount() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lost
}

func (m *Module) startSpillFlusher(ctx context.Context) {
	if m.spill == nil || m.spillCancel != nil {
		return
	}
	flushCtx, cancel := context.WithCancel(ctx)
	m.spillCancel = cancel
	go func() {
		ticker := time.NewTicker(spillFlushTick)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				m.flushSpill(flushCtx)
			case <-m.spill.wake:
				m.flushSpill(flushCtx)
			case <-flushCtx.Done():
				return
			}
		}
	}()
}

func (m *Module) flushSpill(ctx context.Context) {
	if m.repo == nil || m.spill == nil {
		return
	}
	for {
		event, ok := m.spill.peek()
		if !ok {
			return
		}
		if err := m.repo.InsertEvent(ctx, event); err != nil {
			// Preserve FIFO order. The next tick/wake retries the same event;
			// a failed database must not cause later events to overtake it.
			m.logger.Warn("fraud: spill event flush failed", slog.Any("error", err), slog.String("event_id", event.ID.String()))
			return
		}
		m.spill.pop()
		fraudScreeningEventSpillDepth.Set(float64(m.spill.depth()))
	}
}
