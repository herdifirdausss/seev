package retentionworker

import (
	"testing"
	"time"

	"github.com/herdifirdausss/seev/pkg/scheduler"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStart_RejectsUnknownOwnerJitter(t *testing.T) {
	db, _ := newMock(t)
	r, err := NewRunner("widgetservicenotreal", db, nil)
	require.NoError(t, err)

	sched := scheduler.NewScheduler(scheduler.NewMemoryLock(time.Minute), nil, scheduler.WithLocation(JakartaLocation))
	t.Cleanup(sched.Stop)

	err = r.Start(sched)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no registered schedule jitter")
}

func TestStart_EveryRealOwnerHasADistinctJitterMinute(t *testing.T) {
	owners := []string{"adminbff", "assurance", "auth", "fraud", "gateway", "ledger", "payin", "payout", "vendor"}
	seen := map[int]string{}
	for _, o := range owners {
		jitter, ok := serviceJitterMinutes[o]
		if !ok {
			t.Fatalf("owner %q has no entry in serviceJitterMinutes", o)
		}
		if other, dup := seen[jitter]; dup {
			t.Fatalf("owners %q and %q share the same jitter minute %d — schedules would collide", o, other, jitter)
		}
		seen[jitter] = o
	}
}

func TestStart_RegistersWithoutError(t *testing.T) {
	db, _ := newMock(t)
	r, err := NewRunner("ledger", db, nil)
	require.NoError(t, err)

	sched := scheduler.NewScheduler(scheduler.NewMemoryLock(time.Minute), nil, scheduler.WithLocation(JakartaLocation))
	t.Cleanup(sched.Stop)

	require.NoError(t, r.Start(sched))
}
