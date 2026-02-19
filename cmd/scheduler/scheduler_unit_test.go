package main

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ============================================================
// Unit Tests - Calendar Helpers
// ============================================================

func TestLastDayOfMonth(t *testing.T) {
	tests := []struct {
		year  int
		month time.Month
		want  int
	}{
		{2024, time.January, 31},
		{2024, time.February, 29}, // leap year
		{2025, time.February, 28}, // non-leap
		{2024, time.April, 30},
		{2024, time.December, 31},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%d-%02d", tt.year, tt.month), func(t *testing.T) {
			got := lastDayOfMonth(tt.year, tt.month)
			if got != tt.want {
				t.Errorf("lastDayOfMonth(%d, %v) = %d, want %d", tt.year, tt.month, got, tt.want)
			}
		})
	}
}

func TestNearestWeekday(t *testing.T) {
	tests := []struct {
		name  string
		year  int
		month time.Month
		day   int
		want  int
	}{
		// Weekdays remain unchanged
		{"monday unchanged", 2024, time.January, 15, 15}, // Jan 15, 2024 is Monday
		{"tuesday unchanged", 2024, time.January, 16, 16},

		// Saturday -> Friday (if not start of month)
		{"saturday to friday", 2024, time.June, 15, 14}, // Jun 15, 2024 is Saturday
		{"saturday at start", 2024, time.June, 1, 3},    // Jun 1, 2024 is Saturday -> Monday 3

		// Sunday -> Monday (if not end of month)
		{"sunday to monday", 2024, time.September, 15, 16}, // Sep 15, 2024 is Sunday
		{"sunday at end", 2024, time.March, 31, 29},        // Mar 31, 2024 is Sunday -> Friday 29

		// Edge cases
		{"day 0", 2024, time.January, 0, 1},    // clamp to 1
		{"day 32", 2024, time.January, 32, 31}, // clamp to last day
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := nearestWeekday(tt.year, tt.month, tt.day)
			if got != tt.want {
				t.Errorf("nearestWeekday(%d, %v, %d) = %d, want %d",
					tt.year, tt.month, tt.day, got, tt.want)
			}
		})
	}
}

func TestLastWeekdayInMonth(t *testing.T) {
	tests := []struct {
		name    string
		year    int
		month   time.Month
		weekday time.Weekday
		want    int
	}{
		{"last friday jan 2024", 2024, time.January, time.Friday, 26},
		{"last sunday feb 2024", 2024, time.February, time.Sunday, 25},
		{"last monday dec 2024", 2024, time.December, time.Monday, 30},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := lastWeekdayInMonth(tt.year, tt.month, tt.weekday)
			if got != tt.want {
				t.Errorf("lastWeekdayInMonth(%d, %v, %v) = %d, want %d",
					tt.year, tt.month, tt.weekday, got, tt.want)
			}
		})
	}
}

func TestNthWeekdayInMonth(t *testing.T) {
	tests := []struct {
		name    string
		year    int
		month   time.Month
		weekday time.Weekday
		n       int
		want    int
	}{
		{"first monday jan 2024", 2024, time.January, time.Monday, 1, 1},
		{"second tuesday jan 2024", 2024, time.January, time.Tuesday, 2, 9},
		{"third wednesday jan 2024", 2024, time.January, time.Wednesday, 3, 17},
		{"fourth thursday jan 2024", 2024, time.January, time.Thursday, 4, 25},
		{"fifth friday jan 2024", 2024, time.January, time.Friday, 5, 0},  // doesn't exist
		{"fifth monday apr 2024", 2024, time.April, time.Monday, 5, 29},   // exists
		{"fifth monday feb 2024", 2024, time.February, time.Monday, 5, 0}, // doesn't exist
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := nthWeekdayInMonth(tt.year, tt.month, tt.weekday, tt.n)
			if got != tt.want {
				t.Errorf("nthWeekdayInMonth(%d, %v, %v, %d) = %d, want %d",
					tt.year, tt.month, tt.weekday, tt.n, got, tt.want)
			}
		})
	}
}

// ============================================================
// Unit Tests - Field Parsing
// ============================================================

func TestParseCronField(t *testing.T) {
	tests := []struct {
		name    string
		field   string
		min     int
		max     int
		want    []int
		wantErr bool
	}{
		{"wildcard", "*", 0, 5, []int{0, 1, 2, 3, 4, 5}, false},
		{"single value", "3", 0, 5, []int{3}, false},
		{"list", "1,3,5", 0, 5, []int{1, 3, 5}, false},
		{"range", "2-4", 0, 5, []int{2, 3, 4}, false},
		{"step", "*/2", 0, 5, []int{0, 2, 4}, false},
		{"out of range", "6", 0, 5, nil, true},
		{"invalid range", "4-2", 0, 5, nil, true},
		{"invalid step", "*/-1", 0, 5, nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseCronField(tt.field, tt.min, tt.max)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseCronField() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err == nil {
				for _, v := range tt.want {
					if !got.values[v] {
						t.Errorf("expected value %d to be present", v)
					}
				}
			}
		})
	}
}

func TestParseDOMField(t *testing.T) {
	tests := []struct {
		name       string
		field      string
		wantWild   bool
		wantValues []int
		wantL      []int
		wantW      []int
		wantLW     bool
		wantErr    bool
	}{
		{"wildcard", "*", true, nil, nil, nil, false, false},
		{"single day", "15", false, []int{15}, nil, nil, false, false},
		{"list", "1,15,30", false, []int{1, 15, 30}, nil, nil, false, false},
		{"L", "L", false, nil, []int{0}, nil, false, false},
		{"L-3", "L-3", false, nil, []int{3}, nil, false, false},
		{"15W", "15W", false, nil, nil, []int{15}, false, false},
		{"LW", "LW", false, nil, nil, nil, true, false},
		{"combo", "1,15,L,20W", false, []int{1, 15}, []int{0}, []int{20}, false, false},
		{"invalid L", "L-50", false, nil, nil, nil, false, true},
		{"invalid W", "32W", false, nil, nil, nil, false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseDOMField(tt.field)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseDOMField() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err != nil {
				return
			}

			if got.wildcard != tt.wantWild {
				t.Errorf("wildcard = %v, want %v", got.wildcard, tt.wantWild)
			}

			for _, v := range tt.wantValues {
				if !got.values[v] {
					t.Errorf("expected value %d to be present", v)
				}
			}

			if len(got.lastOffsets) != len(tt.wantL) {
				t.Errorf("lastOffsets len = %d, want %d", len(got.lastOffsets), len(tt.wantL))
			}

			if len(got.nearestW) != len(tt.wantW) {
				t.Errorf("nearestW len = %d, want %d", len(got.nearestW), len(tt.wantW))
			}

			if got.hasLW != tt.wantLW {
				t.Errorf("hasLW = %v, want %v", got.hasLW, tt.wantLW)
			}
		})
	}
}

func TestParseDOWField(t *testing.T) {
	tests := []struct {
		name       string
		field      string
		wantWild   bool
		wantValues []int
		wantL      []int
		wantNth    []nthDowEntry
		wantErr    bool
	}{
		{"wildcard", "*", true, nil, nil, nil, false},
		{"single day", "1", false, []int{1}, nil, nil, false},
		{"list", "1,3,5", false, []int{1, 3, 5}, nil, nil, false},
		{"range", "1-5", false, []int{1, 2, 3, 4, 5}, nil, nil, false},
		{"sunday as 7", "7", false, []int{0}, nil, nil, false},
		{"5L", "5L", false, nil, []int{5}, nil, false},
		{"2#3", "2#3", false, nil, nil, []nthDowEntry{{2, 3}}, false},
		{"combo", "1,5L,2#3", false, []int{1}, []int{5}, []nthDowEntry{{2, 3}}, false},
		{"invalid dow", "8", false, nil, nil, nil, true},
		{"invalid #", "2#6", false, nil, nil, nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseDOWField(tt.field)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseDOWField() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err != nil {
				return
			}

			if got.wildcard != tt.wantWild {
				t.Errorf("wildcard = %v, want %v", got.wildcard, tt.wantWild)
			}

			for _, v := range tt.wantValues {
				if !got.values[v] {
					t.Errorf("expected value %d to be present", v)
				}
			}

			if len(got.lastDow) != len(tt.wantL) {
				t.Errorf("lastDow len = %d, want %d", len(got.lastDow), len(tt.wantL))
			}

			if len(got.nthDow) != len(tt.wantNth) {
				t.Errorf("nthDow len = %d, want %d", len(got.nthDow), len(tt.wantNth))
			}
		})
	}
}

// ============================================================
// Unit Tests - Cron Next()
// ============================================================

func TestCronNext_Simple(t *testing.T) {
	loc, _ := time.LoadLocation("UTC")

	tests := []struct {
		name     string
		spec     string
		from     time.Time
		expected time.Time
	}{
		{
			"every minute",
			"* * * * *",
			time.Date(2024, 1, 1, 0, 0, 0, 0, loc),
			time.Date(2024, 1, 1, 0, 1, 0, 0, loc),
		},
		{
			"every hour",
			"0 * * * *",
			time.Date(2024, 1, 1, 0, 0, 0, 0, loc),
			time.Date(2024, 1, 1, 1, 0, 0, 0, loc),
		},
		{
			"every day at noon",
			"0 12 * * *",
			time.Date(2024, 1, 1, 0, 0, 0, 0, loc),
			time.Date(2024, 1, 1, 12, 0, 0, 0, loc),
		},
		{
			"specific minute",
			"15 * * * *",
			time.Date(2024, 1, 1, 0, 0, 0, 0, loc),
			time.Date(2024, 1, 1, 0, 15, 0, 0, loc),
		},
		{
			"skip to next hour",
			"0 * * * *",
			time.Date(2024, 1, 1, 0, 30, 0, 0, loc),
			time.Date(2024, 1, 1, 1, 0, 0, 0, loc),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cron, err := ParseCron(tt.spec)
			if err != nil {
				t.Fatalf("ParseCron() error = %v", err)
			}

			got, err := cron.Next(tt.from, loc)
			if err != nil {
				t.Fatalf("Next() error = %v", err)
			}

			if !got.Equal(tt.expected) {
				t.Errorf("Next() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestCronNext_Ranges(t *testing.T) {
	loc, _ := time.LoadLocation("UTC")

	tests := []struct {
		name     string
		spec     string
		from     time.Time
		expected time.Time
	}{
		{
			"minute range",
			"15-17 * * * *",
			time.Date(2024, 1, 1, 0, 0, 0, 0, loc),
			time.Date(2024, 1, 1, 0, 15, 0, 0, loc),
		},
		{
			"hour range",
			"0 9-17 * * *",
			time.Date(2024, 1, 1, 0, 0, 0, 0, loc),
			time.Date(2024, 1, 1, 9, 0, 0, 0, loc),
		},
		{
			"weekday range",
			"0 9 * * 1-5",
			time.Date(2024, 1, 6, 0, 0, 0, 0, loc), // Saturday
			time.Date(2024, 1, 8, 9, 0, 0, 0, loc), // Monday
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cron, err := ParseCron(tt.spec)
			if err != nil {
				t.Fatalf("ParseCron() error = %v", err)
			}

			got, err := cron.Next(tt.from, loc)
			if err != nil {
				t.Fatalf("Next() error = %v", err)
			}

			if !got.Equal(tt.expected) {
				t.Errorf("Next() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestCronNext_Steps(t *testing.T) {
	loc, _ := time.LoadLocation("UTC")

	cron, err := ParseCron("*/15 * * * *")
	if err != nil {
		t.Fatalf("ParseCron() error = %v", err)
	}

	from := time.Date(2024, 1, 1, 0, 59, 0, 0, loc)

	expected := []int{0, 15, 30, 45, 0, 15} // wraps to next hour
	for i, wantMin := range expected {
		got, err := cron.Next(from, loc)
		if err != nil {
			t.Fatalf("iteration %d: Next() error = %v", i, err)
		}

		if got.Minute() != wantMin {
			t.Errorf("iteration %d: minute = %d, want %d", i, got.Minute(), wantMin)
		}

		from = got
	}
}

// ============================================================
// Unit Tests - Scheduler
// ============================================================

func TestScheduler_BasicOperation(t *testing.T) {
	sched := NewScheduler(NewMemoryLock(time.Minute), nil, WithDefaultTimeout(1*time.Second))
	defer sched.Stop()

	var executed atomic.Bool
	err := sched.Cron("test-job", "* * * * *", func(ctx context.Context) error {
		executed.Store(true)
		return nil
	})

	if err != nil {
		t.Fatalf("Cron() error = %v", err)
	}

	// Job should be in heap
	sched.mu.Lock()
	jobCount := len(sched.jobs)
	sched.mu.Unlock()

	if jobCount != 1 {
		t.Errorf("expected 1 job in heap, got %d", jobCount)
	}
}

func TestScheduler_JobOptions(t *testing.T) {
	sched := NewScheduler(NewMemoryLock(time.Minute), nil, WithDefaultTimeout(1*time.Second))
	defer sched.Stop()

	err := sched.Cron("test-job", "* * * * *", func(ctx context.Context) error {
		return nil
	}, WithJobTimeout(5*time.Second))

	if err != nil {
		t.Fatalf("Cron() error = %v", err)
	}

	sched.mu.Lock()
	if len(sched.jobs) == 0 {
		t.Fatal("no jobs in heap")
	}
	job := sched.jobs[0]
	sched.mu.Unlock()

	if job.Timeout != 5*time.Second {
		t.Errorf("job timeout = %v, want %v", job.Timeout, 5*time.Second)
	}
}

func TestScheduler_InvalidSpec(t *testing.T) {
	sched := NewScheduler(NewMemoryLock(time.Minute), nil)
	defer sched.Stop()

	err := sched.Cron("bad-job", "invalid spec", func(ctx context.Context) error {
		return nil
	})

	if err == nil {
		t.Error("expected error for invalid spec, got nil")
	}
}

func TestScheduler_StopWaitsForJobs(t *testing.T) {
	sched := NewScheduler(NewMemoryLock(time.Minute), nil, WithDefaultTimeout(5*time.Second))

	var started atomic.Bool
	var finished atomic.Bool

	_ = sched.Cron("job", "* * * * *", func(ctx context.Context) error {
		started.Store(true)
		time.Sleep(1 * time.Second)
		finished.Store(true)
		return nil
	})

	// Wait a bit for job to potentially start
	time.Sleep(100 * time.Millisecond)

	// Stop should wait for job to finish
	sched.Stop()

	if started.Load() && !finished.Load() {
		t.Error("Stop() did not wait for running job to complete")
	}
}

// ============================================================
// Unit Tests - MemoryLock
// ============================================================

func TestMemoryLock_Basic(t *testing.T) {
	lock := NewMemoryLock(time.Minute)
	ctx := context.Background()

	// Can acquire free lock
	ok, err := lock.TryLock(ctx, "key1", 1*time.Second)
	if err != nil || !ok {
		t.Fatalf("TryLock() = (%v, %v), want (true, nil)", ok, err)
	}

	// Cannot acquire held lock
	ok, err = lock.TryLock(ctx, "key1", 1*time.Second)
	if err != nil || ok {
		t.Errorf("TryLock() on held lock = (%v, %v), want (false, nil)", ok, err)
	}

	// Can unlock
	err = lock.Unlock(ctx, "key1")
	if err != nil {
		t.Errorf("Unlock() error = %v", err)
	}

	// Can acquire after unlock
	ok, err = lock.TryLock(ctx, "key1", 1*time.Second)
	if err != nil || !ok {
		t.Errorf("TryLock() after unlock = (%v, %v), want (true, nil)", ok, err)
	}
}

func TestMemoryLock_Expiry(t *testing.T) {
	lock := NewMemoryLock(time.Minute)
	ctx := context.Background()

	ok, err := lock.TryLock(ctx, "key1", 100*time.Millisecond)
	if err != nil || !ok {
		t.Fatalf("TryLock() = (%v, %v), want (true, nil)", ok, err)
	}

	// Wait for expiry
	time.Sleep(150 * time.Millisecond)

	// Should be able to acquire again
	ok, err = lock.TryLock(ctx, "key1", 1*time.Second)
	if err != nil || !ok {
		t.Errorf("TryLock() after expiry = (%v, %v), want (true, nil)", ok, err)
	}
}

func TestMemoryLock_Concurrent(t *testing.T) {
	lock := NewMemoryLock(time.Minute)
	ctx := context.Background()

	var counter atomic.Int32
	var wg sync.WaitGroup

	// Multiple goroutines try to acquire same lock
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := lock.TryLock(ctx, "shared", 100*time.Millisecond)
			if err == nil && ok {
				counter.Add(1)
				time.Sleep(50 * time.Millisecond)
				lock.Unlock(ctx, "shared")
			}
		}()
	}

	wg.Wait()

	// Only one should have succeeded at a time
	// But with expiry, multiple might get through
	count := counter.Load()
	if count == 0 {
		t.Error("no goroutine acquired the lock")
	}
}

// ============================================================
// Integration Tests
// ============================================================

func TestIntegration_MultipleJobs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	sched := NewScheduler(NewMemoryLock(time.Minute), nil, WithDefaultTimeout(1*time.Second))
	defer sched.Stop()

	var job1Count, job2Count atomic.Int32

	job1 := testImmediateJob(t, "job1", func(ctx context.Context) error {
		job1Count.Add(1)
		return nil
	})

	job2 := testImmediateJob(t, "job2", func(ctx context.Context) error {
		job2Count.Add(1)
		return nil
	})

	sched.Add(job1)
	sched.Add(job2)

	time.Sleep(2 * time.Second)

	c1 := job1Count.Load()
	c2 := job2Count.Load()

	if c1 == 0 || c2 == 0 {
		t.Errorf("jobs not executed: job1=%d, job2=%d", c1, c2)
	}
}

func TestIntegration_ErrorHandling(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	m := &testMetrics{}
	var metrics Metrics = m

	sched := NewScheduler(NewMemoryLock(time.Minute), metrics, WithDefaultTimeout(1*time.Second))
	defer sched.Stop()

	successJob := testImmediateJob(t, "success-job", func(ctx context.Context) error {
		return nil
	})

	errorJob := testImmediateJob(t, "fail-job", func(ctx context.Context) error {
		return fmt.Errorf("intentional error")
	})

	sched.Add(successJob)
	sched.Add(errorJob)

	time.Sleep(2 * time.Second)

	if m.starts.Load() == 0 {
		t.Error("no jobs started")
	}

	if m.successes.Load() == 0 {
		t.Error("no successful jobs")
	}

	if m.failures.Load() == 0 {
		t.Error("no failed jobs recorded")
	}
}

// ============================================================
// Benchmarks
// ============================================================

func BenchmarkParseCron(b *testing.B) {
	spec := "0 0 15W * 5L"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = ParseCron(spec)
	}
}

func BenchmarkCronNext(b *testing.B) {
	cron, _ := ParseCron("*/5 * * * *")
	loc, _ := time.LoadLocation("UTC")
	now := time.Now()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = cron.Next(now, loc)
	}
}

func BenchmarkCronNextComplex(b *testing.B) {
	cron, _ := ParseCron("0 0 L,15W * 5L,2#3")
	loc, _ := time.LoadLocation("UTC")
	now := time.Now()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = cron.Next(now, loc)
	}
}

func BenchmarkMemoryLockTryLock(b *testing.B) {
	lock := NewMemoryLock(time.Minute)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("key-%d", i%1000)
		lock.TryLock(ctx, key, 1*time.Second)
	}
}

func BenchmarkSchedulerAddUnitTest(b *testing.B) {
	sched := NewScheduler(NewMemoryLock(time.Minute), nil)
	defer sched.Stop()

	cron, _ := ParseCron("*/1 * * * *")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		job := &Job{
			Name:    fmt.Sprintf("job-%d", i),
			Cron:    cron,
			Run:     func(ctx context.Context) error { return nil },
			Timeout: 1 * time.Second,
			NextRun: time.Now().Add(1 * time.Hour),
		}
		sched.Add(job)
	}
}
