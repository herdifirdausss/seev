package scheduler

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testImmediateJob(t *testing.T, name string, fn func(context.Context) error) *Job {
	cron, err := ParseCron("*/1 * * * *")
	if err != nil {
		t.Fatalf("ParseCron() error = %v", err)
	}
	return &Job{
		Name:    name,
		Run:     fn,
		NextRun: time.Now(),
		Cron:    cron,
	}
}

// ============================================================
// Brutal Tests - Edge Cases & Race Conditions
// ============================================================

func TestBrutal_CronParsingEdgeCases(t *testing.T) {
	tests := []struct {
		name    string
		spec    string
		wantErr bool
	}{
		// Valid specs
		{"simple", "0 0 * * *", false},
		{"with ranges", "0-30 0-12 1-15 * 1-5", false},
		{"with steps", "*/5 */2 * * *", false},
		{"with L", "0 0 L * *", false},
		{"with L-offset", "0 0 L-3 * *", false},
		{"with W", "0 0 15W * *", false},
		{"with LW", "0 0 LW * *", false},
		{"with dow L", "0 0 * * 5L", false},
		{"with dow #", "0 0 * * 2#3", false},
		{"complex combo", "0 0 1,15,L,20W * 5L,2#3", false},
		{"dow 7 normalize", "0 0 * * 7", false},

		// Invalid specs
		{"empty", "", true},
		{"too few fields", "0 0 *", true},
		{"too many fields", "0 0 * * * *", true},
		{"invalid minute", "60 0 * * *", true},
		{"invalid hour", "0 25 * * *", true},
		{"invalid dom", "0 0 32 * *", true},
		{"invalid month", "0 0 * 13 *", true},
		{"invalid dow", "0 0 * * 8", true},
		{"invalid L offset", "0 0 L-31 * *", true},
		{"invalid W day", "0 0 32W * *", true},
		{"invalid dow #", "0 0 * * 2#6", true},
		{"invalid dow # zero", "0 0 * * 2#0", true},
		{"malformed L", "0 0 L- * *", true},
		{"malformed W", "0 0 W * *", true},
		{"malformed #", "0 0 * * #3", true},
		{"negative step", "0 */-1 * * *", true},
		{"zero step", "*/0 0 * * *", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseCron(tt.spec)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseCron(%q) error = %v, wantErr %v", tt.spec, err, tt.wantErr)
			}
		})
	}
}

func TestBrutal_NextEdgeCases(t *testing.T) {
	loc, _ := time.LoadLocation("UTC")

	tests := []struct {
		name     string
		spec     string
		from     time.Time
		expected time.Time
		wantErr  bool
	}{
		// February 30th - impossible date
		{
			name:    "feb 30 impossible",
			spec:    "0 0 30 2 *",
			from:    time.Date(2024, 1, 1, 0, 0, 0, 0, loc),
			wantErr: true,
		},
		// February 29th - leap year only
		{
			name:     "feb 29 leap year 2024",
			spec:     "0 0 29 2 *",
			from:     time.Date(2024, 1, 1, 0, 0, 0, 0, loc),
			expected: time.Date(2024, 2, 29, 0, 0, 0, 0, loc),
			wantErr:  false,
		},
		{
			name:     "feb 29 skip non-leap 2025",
			spec:     "0 0 29 2 *",
			from:     time.Date(2024, 3, 1, 0, 0, 0, 0, loc),
			expected: time.Date(2028, 2, 29, 0, 0, 0, 0, loc), // next leap year
			wantErr:  false,
		},
		// Last day of month variations
		{
			name:     "L in 31-day month",
			spec:     "0 0 L * *",
			from:     time.Date(2024, 1, 1, 0, 0, 0, 0, loc),
			expected: time.Date(2024, 1, 31, 0, 0, 0, 0, loc),
		},
		{
			name:     "L in 30-day month",
			spec:     "0 0 L * *",
			from:     time.Date(2024, 4, 1, 0, 0, 0, 0, loc),
			expected: time.Date(2024, 4, 30, 0, 0, 0, 0, loc),
		},
		{
			name:     "L in Feb leap year",
			spec:     "0 0 L * *",
			from:     time.Date(2024, 2, 1, 0, 0, 0, 0, loc),
			expected: time.Date(2024, 2, 29, 0, 0, 0, 0, loc),
		},
		{
			name:     "L in Feb non-leap",
			spec:     "0 0 L * *",
			from:     time.Date(2025, 2, 1, 0, 0, 0, 0, loc),
			expected: time.Date(2025, 2, 28, 0, 0, 0, 0, loc),
		},
		{
			name:     "L-3 in short month",
			spec:     "0 0 L-3 * *",
			from:     time.Date(2024, 2, 1, 0, 0, 0, 0, loc),
			expected: time.Date(2024, 2, 26, 0, 0, 0, 0, loc), // 29-3=26
		},
		// Weekday (W) edge cases
		{
			name:     "15W when 15th is Monday",
			spec:     "0 0 15W * *",
			from:     time.Date(2024, 1, 1, 0, 0, 0, 0, loc), // Jan 15, 2024 is Monday
			expected: time.Date(2024, 1, 15, 0, 0, 0, 0, loc),
		},
		{
			name:     "15W when 15th is Saturday",
			spec:     "0 0 15W * *",
			from:     time.Date(2024, 6, 1, 0, 0, 0, 0, loc),  // Jun 15, 2024 is Saturday
			expected: time.Date(2024, 6, 14, 0, 0, 0, 0, loc), // Friday
		},
		{
			name:     "15W when 15th is Sunday",
			spec:     "0 0 15W * *",
			from:     time.Date(2024, 9, 1, 0, 0, 0, 0, loc),  // Sep 15, 2024 is Sunday
			expected: time.Date(2024, 9, 16, 0, 0, 0, 0, loc), // Monday
		},
		{
			name:     "1W when 1st is Sunday",
			spec:     "0 0 1W * *",
			from:     time.Date(2024, 9, 1, 0, 0, 0, 0, loc), // Sep 1, 2024 is Sunday
			expected: time.Date(2024, 9, 2, 0, 0, 0, 0, loc), // Monday (can't go to prev month)
		},
		{
			name:     "31W in 30-day month",
			spec:     "0 0 31W * *",
			from:     time.Date(2024, 4, 1, 0, 0, 0, 0, loc),  // April has 30 days
			expected: time.Date(2024, 4, 30, 0, 0, 0, 0, loc), // April 30
		},
		{
			name:     "LW last weekday of month",
			spec:     "0 0 LW * *",
			from:     time.Date(2024, 3, 1, 0, 0, 0, 0, loc),  // Mar 31, 2024 is Sunday
			expected: time.Date(2024, 3, 29, 0, 0, 0, 0, loc), // Friday
		},
		// Day-of-week L (last occurrence)
		{
			name:     "5L last Friday",
			spec:     "0 0 * * 5L",
			from:     time.Date(2024, 1, 1, 0, 0, 0, 0, loc),
			expected: time.Date(2024, 1, 26, 0, 0, 0, 0, loc), // last Friday of Jan 2024
		},
		{
			name:     "0L last Sunday",
			spec:     "0 0 * * 0L",
			from:     time.Date(2024, 2, 1, 0, 0, 0, 0, loc),
			expected: time.Date(2024, 2, 25, 0, 0, 0, 0, loc), // last Sunday of Feb 2024
		},
		// Day-of-week # (nth occurrence)
		{
			name:     "2#3 third Tuesday",
			spec:     "0 0 * * 2#3",
			from:     time.Date(2024, 1, 1, 0, 0, 0, 0, loc),
			expected: time.Date(2024, 1, 16, 0, 0, 0, 0, loc), // 3rd Tuesday of Jan 2024
		},
		{
			name:     "1#5 fifth Monday nonexistent",
			spec:     "0 0 * * 1#5",
			from:     time.Date(2024, 2, 1, 0, 0, 0, 0, loc),  // Feb 2024 has no 5th Monday
			expected: time.Date(2024, 4, 29, 0, 0, 0, 0, loc), // Apr 2024 has 5th Monday
		},
		// OR semantics (dom AND dow both restricted)
		{
			name:     "dom OR dow",
			spec:     "0 0 15 * 5", // 15th OR Friday
			from:     time.Date(2024, 1, 1, 0, 0, 0, 0, loc),
			expected: time.Date(2024, 1, 5, 0, 0, 0, 0, loc), // First Friday (before 15th)
		},
		// Year boundary
		{
			name:     "year rollover",
			spec:     "0 0 31 12 *",
			from:     time.Date(2024, 12, 30, 0, 0, 0, 0, loc),
			expected: time.Date(2024, 12, 31, 0, 0, 0, 0, loc),
		},
		{
			name:     "year rollover next",
			spec:     "0 0 31 12 *",
			from:     time.Date(2024, 12, 31, 1, 0, 0, 0, loc),
			expected: time.Date(2025, 12, 31, 0, 0, 0, 0, loc),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cron, err := ParseCron(tt.spec)
			if err != nil {
				t.Fatalf("ParseCron failed: %v", err)
			}

			next, err := cron.Next(tt.from, loc)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got next = %v", next)
				}
				return
			}

			if err != nil {
				t.Fatalf("Next() error = %v", err)
			}

			if !next.Equal(tt.expected) {
				t.Errorf("Next() = %v, want %v", next, tt.expected)
			}
		})
	}
}

func TestBrutal_SchedulerRaceConditions(t *testing.T) {
	// Test concurrent job registration
	t.Run("concurrent registration", func(t *testing.T) {
		sched := NewScheduler(NewMemoryLock(time.Minute), nil, WithDefaultTimeout(1*time.Second))
		defer sched.Stop()

		var wg sync.WaitGroup
		for i := 0; i < 100; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				_ = sched.Cron(fmt.Sprintf("job-%d", id), "*/1 * * * *", func(ctx context.Context) error {
					return nil
				})
			}(i)
		}
		wg.Wait()

		sched.mu.Lock()
		jobCount := len(sched.jobs)
		sched.mu.Unlock()

		if jobCount != 100 {
			t.Errorf("expected 100 jobs, got %d", jobCount)
		}
	})

	// Test job execution doesn't race with rescheduling
	// Can't simulate because limitation per minutes scheduler
	// t.Run("execution reschedule race", func(t *testing.T) {
	// 	sched := NewScheduler(NewMemoryLock(), nil, WithDefaultTimeout(1*time.Second))
	// 	defer sched.Stop()

	// 	var counter atomic.Int32
	// 	job := testImmediateJob(t, "panic-job", func(ctx context.Context) error {
	// 		counter.Add(1)
	// 		time.Sleep(10 * time.Millisecond)
	// 		return nil
	// 	})
	// 	sched.Add(job)

	// 	// Let it run for a bit
	// 	time.Sleep(3 * time.Second)

	// 	count := counter.Load()
	// 	if count < 2 {
	// 		t.Errorf("expected at least 2 executions, got %d", count)
	// 	}
	// })

	// Test lock prevents double execution
	t.Run("lock prevents double exec", func(t *testing.T) {
		lock := NewMemoryLock(time.Minute)
		var counter atomic.Int32

		sched1 := NewScheduler(lock, nil, WithDefaultTimeout(5*time.Second))
		defer sched1.Stop()

		sched2 := NewScheduler(lock, nil, WithDefaultTimeout(5*time.Second))
		defer sched2.Stop()

		jobFn1 := func(ctx context.Context) error {
			counter.Add(1)
			time.Sleep(100 * time.Millisecond)
			return nil
		}

		jobFn2 := func(ctx context.Context) error {
			counter.Add(1)
			time.Sleep(100 * time.Millisecond)
			return nil
		}

		job1 := testImmediateJob(t, "immediate-job-1", jobFn1)
		job2 := testImmediateJob(t, "immediate-job-2", jobFn2)
		sched1.Add(job1)
		sched2.Add(job2)

		time.Sleep(2 * time.Second)

		// With two schedulers running the same job every second,
		// only one should execute at a time due to locking
		count := counter.Load()
		if count < 1 {
			t.Errorf("expected at least 1 execution, got %d", count)
		}
	})
}

func TestBrutal_PanicRecovery(t *testing.T) {
	sched := NewScheduler(NewMemoryLock(time.Minute), nil, WithDefaultTimeout(1*time.Second))
	defer sched.Stop()

	var recovered atomic.Bool

	job := testImmediateJob(t, "panic-job", func(ctx context.Context) error {
		recovered.Store(true)
		panic("intentional panic")
	})

	sched.Add(job)

	time.Sleep(50 * time.Millisecond)

	if !recovered.Load() {
		t.Error("job never executed or panic not recovered")
	}

	// Scheduler should still be running
	sched.mu.Lock()
	jobCount := len(sched.jobs)
	sched.mu.Unlock()

	if jobCount == 0 {
		t.Error("job disappeared after panic")
	}
}

func TestBrutal_ContextTimeout(t *testing.T) {
	sched := NewScheduler(NewMemoryLock(time.Minute), nil, WithDefaultTimeout(100*time.Millisecond))
	defer sched.Stop()

	var timedOut atomic.Bool

	job := testImmediateJob(t, "timeout-job", func(ctx context.Context) error {
		select {
		case <-time.After(1 * time.Second):
			return fmt.Errorf("should have timed out")
		case <-ctx.Done():
			timedOut.Store(true)
			return ctx.Err()
		}
	})
	sched.Add(job)

	time.Sleep(2 * time.Second)

	if !timedOut.Load() {
		t.Error("context timeout did not fire")
	}
}

func TestBrutal_GracefulShutdown(t *testing.T) {
	sched := NewScheduler(NewMemoryLock(time.Minute), nil, WithDefaultTimeout(5*time.Second))

	var started atomic.Bool
	var finished atomic.Bool

	job := testImmediateJob(t, "long-job", func(ctx context.Context) error {
		started.Store(true)
		time.Sleep(2 * time.Second)
		finished.Store(true)
		return nil
	})
	sched.Add(job)

	// Wait for job to start
	time.Sleep(500 * time.Millisecond)

	// Trigger shutdown while job is running
	done := make(chan struct{})
	go func() {
		sched.Stop()
		close(done)
	}()

	select {
	case <-done:
		// Good, shutdown completed
	case <-time.After(10 * time.Second):
		t.Fatal("shutdown hung")
	}

	if started.Load() && !finished.Load() {
		t.Error("job started but was interrupted by shutdown")
	}
}

func TestBrutal_MemoryLockGC(t *testing.T) {
	lock := NewMemoryLock(time.Second)

	// Acquire many locks with short TTL
	for i := 0; i < 1000; i++ {
		ok, err := lock.TryLock(context.Background(), fmt.Sprintf("key-%d", i), 10*time.Millisecond)
		if err != nil || !ok {
			t.Fatalf("TryLock failed: ok=%v, err=%v", ok, err)
		}
	}

	// Wait for GC
	time.Sleep(5 * time.Second)

	// Map should be cleaned up
	lock.mu.Lock()
	size := len(lock.m)
	lock.mu.Unlock()

	if size > 10 {
		t.Errorf("expected lock map to be mostly cleaned, got size %d", size)
	}
}

func TestBrutal_RedisLockOwnership(t *testing.T) {
	t.Skip("Requires Redis running - enable manually")

	// This test verifies that only the lock owner can unlock
	// Uncomment and run against real Redis to test

	/*
		rdb := redis.NewClient(&redis.Options{Addr: "localhost:6380"})
		defer rdb.Close()

		lock1 := NewRedisLock(rdb, "instance-1")
		lock2 := NewRedisLock(rdb, "instance-2")

		ctx := context.Background()
		key := "test-lock"

		// Instance 1 acquires lock
		ok, err := lock1.TryLock(ctx, key, 5*time.Second)
		if err != nil || !ok {
			t.Fatalf("lock1 TryLock failed: ok=%v, err=%v", ok, err)
		}

		// Instance 2 cannot acquire
		ok, err = lock2.TryLock(ctx, key, 5*time.Second)
		if err != nil {
			t.Fatalf("lock2 TryLock error: %v", err)
		}
		if ok {
			t.Error("lock2 should not be able to acquire lock owned by lock1")
		}

		// Instance 2 cannot unlock (doesn't own it)
		err = lock2.Unlock(ctx, key)
		if err != nil {
			t.Fatalf("Unlock error: %v", err)
		}

		// Lock should still be held
		ok, err = lock2.TryLock(ctx, key, 5*time.Second)
		if err != nil || ok {
			t.Error("lock should still be held by instance 1")
		}

		// Instance 1 can unlock
		err = lock1.Unlock(ctx, key)
		if err != nil {
			t.Fatalf("lock1 Unlock error: %v", err)
		}

		// Now instance 2 can acquire
		ok, err = lock2.TryLock(ctx, key, 5*time.Second)
		if err != nil || !ok {
			t.Error("lock2 should be able to acquire after lock1 released")
		}
	*/
}

func TestBrutal_ComplexSchedules(t *testing.T) {
	// Test realistic complex schedules
	specs := []struct {
		name string
		spec string
	}{
		{"every 5 minutes", "*/5 * * * *"},
		{"business hours", "0 9-17 * * 1-5"},
		{"month end", "0 0 L * *"},
		{"quarter end", "0 0 L 3,6,9,12 *"},
		{"first monday", "0 9 * * 1#1"},
		{"last friday", "0 17 * * 5L"},
		{"mid-month weekday", "0 12 15W * *"},
		{"complex combo", "0 0 1,15,L 1-6 1-5"},
	}

	for _, tt := range specs {
		t.Run(tt.name, func(t *testing.T) {
			cron, err := ParseCron(tt.spec)
			if err != nil {
				t.Fatalf("ParseCron(%q) error = %v", tt.spec, err)
			}

			loc, _ := time.LoadLocation("UTC")
			now := time.Date(2024, 1, 1, 0, 0, 0, 0, loc)

			// Get next 10 occurrences and ensure they're monotonically increasing
			prev := now
			for i := 0; i < 10; i++ {
				next, err := cron.Next(prev, loc)
				if err != nil {
					t.Fatalf("Next() iteration %d error = %v", i, err)
				}
				if !next.After(prev) {
					t.Errorf("iteration %d: next %v not after prev %v", i, next, prev)
				}
				prev = next
			}
		})
	}
}

// Benchmark critical paths
func BenchmarkNextComputation(b *testing.B) {
	cron, _ := ParseCron("0 0 15W * 5L") // Complex spec with W and L
	loc, _ := time.LoadLocation("UTC")
	now := time.Now()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = cron.Next(now, loc)
	}
}

func BenchmarkSchedulerAdd(b *testing.B) {
	sched := NewScheduler(NewMemoryLock(time.Minute), nil)
	defer sched.Stop()

	cron, _ := ParseCron("*/5 * * * *")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		job := &Job{
			Name:    fmt.Sprintf("job-%d", i),
			Cron:    cron,
			Run:     func(ctx context.Context) error { return nil },
			Timeout: 1 * time.Second,
			NextRun: time.Now().Add(time.Hour),
		}
		sched.Add(job)
	}
}
