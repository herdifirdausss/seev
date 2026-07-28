package worker

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type immediateRelayDispatcher struct {
	failedCh chan struct{}
	once     sync.Once
}

func (d *immediateRelayDispatcher) DispatchPendingCommands(context.Context, int) (int, error) {
	return 0, nil
}

func (d *immediateRelayDispatcher) DispatchFailedCommandsForRetry(context.Context, int) (int, error) {
	d.once.Do(func() { close(d.failedCh) })
	return 0, nil
}

func (d *immediateRelayDispatcher) ReapStuckCommands(context.Context, time.Duration) (int, error) {
	return 0, nil
}

func (d *immediateRelayDispatcher) CountCommandsByStatuses(context.Context, []string) (map[string]int, error) {
	return nil, nil
}

var _ dispatcher = (*immediateRelayDispatcher)(nil)

func TestVendorRelayConfig_DefaultsRecoverCrashedCommandsWithinDrillWindow(t *testing.T) {
	cfg := VendorRelayConfig{}
	cfg.applyDefaults()

	require.Equal(t, defaultDispatchPollInterval, cfg.PollInterval)
	require.Equal(t, defaultDispatchRetryInterval, cfg.RetryInterval)
	require.Equal(t, 30*time.Second, cfg.ReaperInterval)
	require.Equal(t, 2*time.Minute, cfg.StuckAfter)
	require.Equal(t, defaultDispatchBatchSize, cfg.BatchSize)
	require.Less(t, cfg.ReaperInterval+cfg.StuckAfter, 5*time.Minute)
}

func TestVendorRelayConfig_ExplicitLeaseTimingsArePreserved(t *testing.T) {
	cfg := VendorRelayConfig{
		ReaperInterval: 11 * time.Second,
		StuckAfter:     47 * time.Second,
	}
	cfg.applyDefaults()

	require.Equal(t, 11*time.Second, cfg.ReaperInterval)
	require.Equal(t, 47*time.Second, cfg.StuckAfter)
}

func TestVendorRelay_StartDispatchesFailedCommandsImmediately(t *testing.T) {
	d := &immediateRelayDispatcher{failedCh: make(chan struct{})}
	relay := NewVendorRelay(d, slog.Default(), VendorRelayConfig{
		PollInterval:   time.Hour,
		RetryInterval:  time.Hour,
		ReaperInterval: time.Hour,
		StuckAfter:     time.Hour,
	})
	relay.Start(context.Background())
	defer relay.Stop()

	select {
	case <-d.failedCh:
	case <-time.After(time.Second):
		t.Fatal("relay did not run the failed-command recovery pass at startup")
	}
}
