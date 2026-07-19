package worker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/herdifirdausss/seev/pkg/scheduler"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type fakeResumer struct {
	resumed, failed int
	resumeErr       error
	counts          map[string]int
	countErr        error
}

func (f *fakeResumer) ResumeStuck(context.Context, time.Duration) (int, int, error) {
	return f.resumed, f.failed, f.resumeErr
}

func (f *fakeResumer) CountStuck(context.Context, time.Duration) (map[string]int, error) {
	return f.counts, f.countErr
}

// TestResumeJob_RefreshesStuckGaugePerTick proves docs/plan/43 K5's
// "one grouped-count query per tick" contract at the job level: every
// status CountStuck returns is set on the gauge after a run, backlog sizes
// well above ResumeStuck's own 100-row batch cap included (the gauge must
// report the true backlog, not the bounded resume-pass count).
func TestResumeJob_RefreshesStuckGaugePerTick(t *testing.T) {
	r := &fakeResumer{
		resumed: 5, failed: 1,
		counts: map[string]int{"created": 3, "held": 0, "submitted": 250, "vendor_pending": 12},
	}
	job := NewResumeJob(r, scheduler.NewMemoryLock(time.Second), discardLogger(), time.Minute)

	require.NoError(t, job.runOnce(context.Background()))

	assert.Equal(t, float64(3), testutil.ToFloat64(stuckRequests.WithLabelValues("created")))
	assert.Equal(t, float64(0), testutil.ToFloat64(stuckRequests.WithLabelValues("held")))
	assert.Equal(t, float64(250), testutil.ToFloat64(stuckRequests.WithLabelValues("submitted")),
		"gauge must reflect the FULL backlog, not ResumeStuck's own 100-row-per-pass cap")
	assert.Equal(t, float64(12), testutil.ToFloat64(stuckRequests.WithLabelValues("vendor_pending")))
}

// TestResumeJob_StuckGauge_ZeroResetsPreviousBacklog proves a status that
// drains to zero is reported as 0 on the next tick, not left stale at its
// last nonzero value (docs/plan/43 K5: "mengisi 0 untuk status yang tidak
// kembali" applies at the repository layer; this proves the gauge itself
// tracks the drain down to zero once the repository reports it).
func TestResumeJob_StuckGauge_ZeroResetsPreviousBacklog(t *testing.T) {
	r := &fakeResumer{counts: map[string]int{"submitted": 40}}
	job := NewResumeJob(r, scheduler.NewMemoryLock(time.Second), discardLogger(), time.Minute)
	require.NoError(t, job.runOnce(context.Background()))
	assert.Equal(t, float64(40), testutil.ToFloat64(stuckRequests.WithLabelValues("submitted")))

	r.counts = map[string]int{"submitted": 0}
	require.NoError(t, job.runOnce(context.Background()))
	assert.Equal(t, float64(0), testutil.ToFloat64(stuckRequests.WithLabelValues("submitted")))
}

// TestResumeJob_CountStuckError_DoesNotFailTheRun proves a gauge-refresh
// failure is logged and swallowed, never propagated as the cron job's own
// result — the resume job's real job (re-driving stuck requests) must
// never fail because of an observability side-channel.
func TestResumeJob_CountStuckError_DoesNotFailTheRun(t *testing.T) {
	r := &fakeResumer{countErr: errors.New("db down")}
	job := NewResumeJob(r, scheduler.NewMemoryLock(time.Second), discardLogger(), time.Minute)
	assert.NoError(t, job.runOnce(context.Background()))
}
