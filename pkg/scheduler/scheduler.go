// Package scheduler implements a production-ready cron scheduler with advanced features:
//   - Extended cron syntax: W (nearest weekday), L (last), # (nth occurrence)
//   - Distributed locking via Redis or in-memory
//   - Graceful shutdown with job completion
//   - Panic recovery per job
//   - Context-based timeout
//   - Pluggable metrics interface
//
// Example usage:
//
//	sched := scheduler.NewScheduler(scheduler.NewMemoryLock(time.Second), nil, scheduler.WithLocation(jakartaLoc))
//	sched.Cron("backup", "0 2 * * *", func(ctx context.Context) error {
//	    return runBackup(ctx)
//	})
//	// Graceful shutdown:
//	sched.Stop()
package scheduler

import (
	"container/heap"
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

// ============================================================
// Calendar Helpers
// ============================================================

// lastDayOfMonth returns the last day (1-31) of the given month/year.
// Uses the property that day 0 of next month = last day of current month.
func lastDayOfMonth(year int, month time.Month) int {
	return time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

// nearestWeekday returns the nearest weekday (Mon-Fri) to targetDay,
// without crossing month boundaries. Rules:
//   - Weekday: return as-is
//   - Saturday: Friday (or Monday if Friday is in previous month)
//   - Sunday: Monday (or Friday if Monday is in next month)
func nearestWeekday(year int, month time.Month, targetDay int) int {
	lastDay := lastDayOfMonth(year, month)

	// Clamp to valid day range
	if targetDay < 1 {
		targetDay = 1
	}
	if targetDay > lastDay {
		targetDay = lastDay
	}

	t := time.Date(year, month, targetDay, 0, 0, 0, 0, time.UTC)
	switch t.Weekday() {
	case time.Saturday:
		// Try Friday (day-1), but not if it goes to previous month
		if targetDay > 1 {
			return targetDay - 1
		}
		// Friday is in previous month, use Monday instead
		if targetDay+2 <= lastDay {
			return targetDay + 2
		}
		return targetDay // edge case: 1st is Saturday and only 1-2 days in month

	case time.Sunday:
		// Try Monday (day+1), but not if it goes to next month
		if targetDay < lastDay {
			return targetDay + 1
		}
		// Monday is in next month, use Friday instead
		if targetDay-2 >= 1 {
			return targetDay - 2
		}
		return targetDay // edge case: last day is Sunday and only 1-2 days in month

	default:
		return targetDay
	}
}

// lastWeekdayInMonth returns the day-of-month (1-31) of the last occurrence
// of the given weekday in the month.
// Example: lastWeekdayInMonth(2024, January, Friday) = 26
func lastWeekdayInMonth(year int, month time.Month, weekday time.Weekday) int {
	lastDay := lastDayOfMonth(year, month)
	t := time.Date(year, month, lastDay, 0, 0, 0, 0, time.UTC)

	// How many days back from lastDay to reach the target weekday?
	diff := (int(t.Weekday()) - int(weekday) + 7) % 7
	return lastDay - diff
}

// nthWeekdayInMonth returns the day-of-month of the nth occurrence (1-based)
// of the given weekday. Returns 0 if the nth occurrence doesn't exist in the month.
// Example: nthWeekdayInMonth(2024, January, Tuesday, 3) = 16 (3rd Tuesday)
func nthWeekdayInMonth(year int, month time.Month, weekday time.Weekday, n int) int {
	if n < 1 || n > 5 {
		return 0
	}

	first := time.Date(year, month, 1, 0, 0, 0, 0, time.UTC)

	// Days from month start to first occurrence of weekday
	diff := (int(weekday) - int(first.Weekday()) + 7) % 7

	// Day-of-month of nth occurrence
	day := 1 + diff + (n-1)*7

	if day > lastDayOfMonth(year, month) {
		return 0 // doesn't exist
	}
	return day
}

// ============================================================
// Field Types
// ============================================================

// cronField represents a standard numeric cron field (minutes, hours, month).
// The wildcard flag is needed for correct dom/dow OR-semantics.
type cronField struct {
	values   map[int]bool
	wildcard bool
}

func (cf cronField) matches(v int) bool {
	return cf.values[v]
}

// parseCronField parses a standard cron field with support for:
//   - Wildcards: *
//   - Steps: */5
//   - Ranges: 1-5
//   - Lists: 1,3,5
func parseCronField(field string, min, max int) (cronField, error) {
	cf := cronField{values: make(map[int]bool)}

	if field == "*" {
		cf.wildcard = true
		for i := min; i <= max; i++ {
			cf.values[i] = true
		}
		return cf, nil
	}

	if strings.HasPrefix(field, "*/") {
		step, err := strconv.Atoi(field[2:])
		if err != nil || step <= 0 {
			return cf, fmt.Errorf("invalid step %q", field)
		}
		for i := min; i <= max; i += step {
			cf.values[i] = true
		}
		return cf, nil
	}

	for _, part := range strings.Split(field, ",") {
		part = strings.TrimSpace(part)

		if strings.Contains(part, "-") {
			// Range: 1-5
			bounds := strings.SplitN(part, "-", 2)
			start, err1 := strconv.Atoi(bounds[0])
			end, err2 := strconv.Atoi(bounds[1])

			if err1 != nil || err2 != nil {
				return cf, fmt.Errorf("invalid range %q", part)
			}
			if start > end {
				return cf, fmt.Errorf("invalid range %q (start > end)", part)
			}
			if start < min || end > max {
				return cf, fmt.Errorf("range %q out of bounds (%d-%d)", part, min, max)
			}

			for i := start; i <= end; i++ {
				cf.values[i] = true
			}
		} else {
			// Single value
			v, err := strconv.Atoi(part)
			if err != nil {
				return cf, fmt.Errorf("invalid value %q", part)
			}
			if v < min || v > max {
				return cf, fmt.Errorf("value %d out of range (%d-%d)", v, min, max)
			}
			cf.values[v] = true
		}
	}

	return cf, nil
}

// domField represents the day-of-month field with extended syntax:
//   - L: last day of month
//   - L-n: n days before last day
//   - nW: nearest weekday to day n
//   - LW: nearest weekday to last day of month
type domField struct {
	wildcard    bool
	values      map[int]bool // regular values 1-31
	lastOffsets []int        // L -> [0], L-3 -> [3]
	nearestW    []int        // nW -> [n]
	hasLW       bool         // LW flag
}

func (d *domField) isRestricted() bool {
	return !d.wildcard
}

func (d *domField) matches(t time.Time) bool {
	if d.wildcard {
		return true
	}

	day := t.Day()
	year := t.Year()
	month := t.Month()
	lastDay := lastDayOfMonth(year, month)

	// Check regular values
	if d.values[day] {
		return true
	}

	// Check L offsets (L, L-3, etc)
	for _, offset := range d.lastOffsets {
		if day == lastDay-offset {
			return true
		}
	}

	// Check nW (nearest weekday to day n)
	for _, n := range d.nearestW {
		if day == nearestWeekday(year, month, n) {
			return true
		}
	}

	// Check LW (nearest weekday to last day)
	if d.hasLW && day == nearestWeekday(year, month, lastDay) {
		return true
	}

	return false
}

func parseDOMField(field string) (*domField, error) {
	df := &domField{values: make(map[int]bool)}

	if field == "*" {
		df.wildcard = true
		return df, nil
	}

	if strings.HasPrefix(field, "*/") {
		step, err := strconv.Atoi(field[2:])
		if err != nil || step <= 0 {
			return nil, fmt.Errorf("invalid step %q", field)
		}
		for i := 1; i <= 31; i += step {
			df.values[i] = true
		}
		return df, nil
	}

	for _, token := range strings.Split(field, ",") {
		token = strings.TrimSpace(token)

		switch {
		case token == "L":
			df.lastOffsets = append(df.lastOffsets, 0)

		case token == "LW":
			df.hasLW = true

		case strings.HasPrefix(token, "L-"):
			n, err := strconv.Atoi(token[2:])
			if err != nil || n < 0 || n > 30 {
				return nil, fmt.Errorf("invalid L offset %q (must be 0-30)", token)
			}
			df.lastOffsets = append(df.lastOffsets, n)

		case strings.HasSuffix(token, "W"):
			n, err := strconv.Atoi(token[:len(token)-1])
			if err != nil || n < 1 || n > 31 {
				return nil, fmt.Errorf("invalid W day %q (must be 1-31)", token)
			}
			df.nearestW = append(df.nearestW, n)

		case strings.Contains(token, "-"):
			bounds := strings.SplitN(token, "-", 2)
			start, err1 := strconv.Atoi(bounds[0])
			end, err2 := strconv.Atoi(bounds[1])

			if err1 != nil || err2 != nil {
				return nil, fmt.Errorf("invalid range %q", token)
			}
			if start > end {
				return nil, fmt.Errorf("invalid range %q (start > end)", token)
			}
			if start < 1 || end > 31 {
				return nil, fmt.Errorf("range %q out of bounds (1-31)", token)
			}

			for i := start; i <= end; i++ {
				df.values[i] = true
			}

		default:
			v, err := strconv.Atoi(token)
			if err != nil {
				return nil, fmt.Errorf("invalid day value %q", token)
			}
			if v < 1 || v > 31 {
				return nil, fmt.Errorf("day value %d out of range (1-31)", v)
			}
			df.values[v] = true
		}
	}

	return df, nil
}

// dowField represents the day-of-week field with extended syntax:
//   - nL: last occurrence of weekday n (e.g., 5L = last Friday)
//   - n#m: mth occurrence of weekday n (e.g., 2#3 = 3rd Tuesday)
//
// Weekday convention: 0=Sunday, 1=Monday, ..., 6=Saturday
// Special: 7 is normalized to 0 (Sunday)
type nthDowEntry struct {
	dow int // 0-6
	n   int // 1-5
}

type dowField struct {
	wildcard bool
	values   map[int]bool  // regular values 0-6
	lastDow  []int         // nL entries
	nthDow   []nthDowEntry // n#m entries
}

func (d *dowField) isRestricted() bool {
	return !d.wildcard
}

func (d *dowField) matches(t time.Time) bool {
	if d.wildcard {
		return true
	}

	weekday := int(t.Weekday())
	year := t.Year()
	month := t.Month()
	day := t.Day()

	// Check regular values
	if d.values[weekday] {
		return true
	}

	// Check nL (last occurrence of weekday n)
	for _, dow := range d.lastDow {
		if weekday == dow {
			expectedDay := lastWeekdayInMonth(year, month, time.Weekday(dow))
			if day == expectedDay {
				return true
			}
		}
	}

	// Check n#m (mth occurrence of weekday n)
	for _, e := range d.nthDow {
		if weekday == e.dow {
			expectedDay := nthWeekdayInMonth(year, month, time.Weekday(e.dow), e.n)
			// expectedDay = 0 means nth occurrence doesn't exist this month
			if expectedDay > 0 && day == expectedDay {
				return true
			}
		}
	}

	return false
}

func parseDOWField(field string) (*dowField, error) {
	df := &dowField{values: make(map[int]bool)}

	// Normalize 7 (Sunday) to 0
	normalizeDOW := func(v int) (int, error) {
		if v == 7 {
			v = 0
		}
		if v < 0 || v > 6 {
			return 0, fmt.Errorf("weekday %d out of range (0-7)", v)
		}
		return v, nil
	}

	if field == "*" {
		df.wildcard = true
		return df, nil
	}

	if strings.HasPrefix(field, "*/") {
		step, err := strconv.Atoi(field[2:])
		if err != nil || step <= 0 {
			return nil, fmt.Errorf("invalid step %q", field)
		}
		for i := 0; i <= 6; i += step {
			df.values[i] = true
		}
		return df, nil
	}

	for _, token := range strings.Split(field, ",") {
		token = strings.TrimSpace(token)

		switch {
		case strings.Contains(token, "#"):
			// n#m: mth occurrence of weekday n
			parts := strings.SplitN(token, "#", 2)
			if len(parts) != 2 {
				return nil, fmt.Errorf("invalid # syntax %q", token)
			}

			rawDow, err1 := strconv.Atoi(parts[0])
			n, err2 := strconv.Atoi(parts[1])
			if err1 != nil || err2 != nil {
				return nil, fmt.Errorf("invalid # syntax %q", token)
			}

			dow, err := normalizeDOW(rawDow)
			if err != nil {
				return nil, fmt.Errorf("# syntax %q: %w", token, err)
			}

			if n < 1 || n > 5 {
				return nil, fmt.Errorf("# occurrence %d out of range (1-5) in %q", n, token)
			}

			df.nthDow = append(df.nthDow, nthDowEntry{dow: dow, n: n})

		case strings.HasSuffix(token, "L"):
			// nL: last occurrence of weekday n
			rawDow, err := strconv.Atoi(token[:len(token)-1])
			if err != nil {
				return nil, fmt.Errorf("invalid L syntax %q", token)
			}

			dow, err := normalizeDOW(rawDow)
			if err != nil {
				return nil, fmt.Errorf("L syntax %q: %w", token, err)
			}

			df.lastDow = append(df.lastDow, dow)

		case strings.Contains(token, "-"):
			// Range: 1-5
			bounds := strings.SplitN(token, "-", 2)
			rawStart, err1 := strconv.Atoi(bounds[0])
			rawEnd, err2 := strconv.Atoi(bounds[1])

			if err1 != nil || err2 != nil {
				return nil, fmt.Errorf("invalid range %q", token)
			}

			start, err := normalizeDOW(rawStart)
			if err != nil {
				return nil, fmt.Errorf("range start in %q: %w", token, err)
			}

			end, err := normalizeDOW(rawEnd)
			if err != nil {
				return nil, fmt.Errorf("range end in %q: %w", token, err)
			}

			if start > end {
				return nil, fmt.Errorf("invalid range %q (start > end)", token)
			}

			for i := start; i <= end; i++ {
				df.values[i] = true
			}

		default:
			// Single weekday value
			rawDow, err := strconv.Atoi(token)
			if err != nil {
				return nil, fmt.Errorf("invalid weekday %q", token)
			}

			dow, err := normalizeDOW(rawDow)
			if err != nil {
				return nil, err
			}

			df.values[dow] = true
		}
	}

	return df, nil
}

// ============================================================
// Cron Expression
// ============================================================

// Cron represents a parsed 5-field cron expression.
// Format: "minute hour day-of-month month day-of-week"
//
// Extended syntax support:
//   - Day-of-month: L, L-n, nW, LW
//   - Day-of-week: nL, n#m
//
// Examples:
//   - "0 9 15W * *" → 9am on nearest weekday to 15th
//   - "0 0 L * *" → midnight on last day of month
//   - "0 8 * * 5L" → 8am on last Friday of month
//   - "0 10 * * 2#3" → 10am on 3rd Tuesday of month
type Cron struct {
	minutes cronField
	hours   cronField
	dom     *domField
	month   cronField
	dow     *dowField
}

// ParseCron parses a 5-field cron expression.
// Returns an error if the syntax is invalid.
func ParseCron(spec string) (*Cron, error) {
	parts := strings.Fields(spec)
	if len(parts) != 5 {
		return nil, fmt.Errorf("invalid cron: expected 5 fields, got %d in %q", len(parts), spec)
	}

	minutes, err := parseCronField(parts[0], 0, 59)
	if err != nil {
		return nil, fmt.Errorf("minute field: %w", err)
	}

	hours, err := parseCronField(parts[1], 0, 23)
	if err != nil {
		return nil, fmt.Errorf("hour field: %w", err)
	}

	dom, err := parseDOMField(parts[2])
	if err != nil {
		return nil, fmt.Errorf("day-of-month field: %w", err)
	}

	month, err := parseCronField(parts[3], 1, 12)
	if err != nil {
		return nil, fmt.Errorf("month field: %w", err)
	}

	dow, err := parseDOWField(parts[4])
	if err != nil {
		return nil, fmt.Errorf("day-of-week field: %w", err)
	}

	return &Cron{
		minutes: minutes,
		hours:   hours,
		dom:     dom,
		month:   month,
		dow:     dow,
	}, nil
}

// maxNextIter limits the number of iterations in Next() to prevent
// infinite loops for impossible specs like "0 0 30 2 *" (Feb 30).
// 4 years in minutes is sufficient for any valid spec.
const maxNextIter = 366 * 4 * 24 * 60

// Next returns the next execution time after t.
// Returns an error if no valid time is found within ~4 years.
//
// Day-of-month / day-of-week semantics (vixie-cron compatible):
//   - Both restricted (non-wildcard) → OR (match either)
//   - Only dom restricted → dom only
//   - Only dow restricted → dow only
//   - Both wildcard → all days match
func (c *Cron) Next(t time.Time, loc *time.Location) (time.Time, error) {
	if loc != nil {
		t = t.In(loc)
	}

	// Start from next minute (truncate seconds)
	t = t.Add(time.Minute).Truncate(time.Minute)

	for i := 0; i < maxNextIter; i++ {
		// ── Month ────────────────────────────────────────────────
		if !c.month.matches(int(t.Month())) {
			// Jump to 1st of next month
			t = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, t.Location())
			continue
		}

		// ── Day (OR-semantics between dom and dow) ──────────────
		domRestricted := c.dom.isRestricted()
		dowRestricted := c.dow.isRestricted()

		var dayOK bool
		switch {
		case domRestricted && dowRestricted:
			// Both restricted → OR (either match is sufficient)
			dayOK = c.dom.matches(t) || c.dow.matches(t)
		case domRestricted:
			// Only dom restricted
			dayOK = c.dom.matches(t)
		case dowRestricted:
			// Only dow restricted
			dayOK = c.dow.matches(t)
		default:
			// Both wildcard → all days match
			dayOK = true
		}

		if !dayOK {
			// Jump to next day
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, t.Location())
			continue
		}

		// ── Hour ─────────────────────────────────────────────────
		if !c.hours.matches(t.Hour()) {
			t = t.Truncate(time.Hour).Add(time.Hour)
			continue
		}

		// ── Minute ───────────────────────────────────────────────
		if !c.minutes.matches(t.Minute()) {
			t = t.Add(time.Minute)
			continue
		}

		return t, nil
	}

	return time.Time{}, fmt.Errorf("no valid next time found within ~4 years for spec")
}

// ============================================================
// Job & Heap
// ============================================================

// Job represents a scheduled cron job.
type Job struct {
	Name    string
	Cron    *Cron
	Run     func(ctx context.Context) error
	Timeout time.Duration
	NextRun time.Time
	index   int // heap position (managed by container/heap)
}

type jobHeap []*Job

func (h jobHeap) Len() int           { return len(h) }
func (h jobHeap) Less(i, j int) bool { return h[i].NextRun.Before(h[j].NextRun) }
func (h jobHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}
func (h *jobHeap) Push(x any) {
	j := x.(*Job)
	j.index = len(*h)
	*h = append(*h, j)
}
func (h *jobHeap) Pop() any {
	old := *h
	n := len(old)
	j := old[n-1]
	old[n-1] = nil // prevent memory leak
	*h = old[:n-1]
	return j
}

// ============================================================
// Interfaces
// ============================================================

// LockProvider abstracts distributed locking.
// Implementations: MemoryLock (single-node), RedisLock (multi-node).
type LockProvider interface {
	TryLock(ctx context.Context, key string, ttl time.Duration) (bool, error)
	Unlock(ctx context.Context, key string) error
}

// Metrics abstracts observability (Prometheus, Datadog, etc).
type Metrics interface {
	JobStart(name string)
	JobSuccess(name string, dur time.Duration)
	JobFail(name string, dur time.Duration, err error)
	JobSkip(name string)
}

type noopMetrics struct{}

func (noopMetrics) JobStart(string)                      {}
func (noopMetrics) JobSuccess(string, time.Duration)     {}
func (noopMetrics) JobFail(string, time.Duration, error) {}
func (noopMetrics) JobSkip(string)                       {}

type testMetrics struct {
	starts    atomic.Int64
	successes atomic.Int64
	failures  atomic.Int64
	skips     atomic.Int64
}

func (t *testMetrics) JobStart(name string) {
	t.starts.Add(1)
}

func (t *testMetrics) JobSuccess(name string, dur time.Duration) {
	t.successes.Add(1)
}

func (t *testMetrics) JobFail(name string, dur time.Duration, err error) {
	t.failures.Add(1)
}

func (t *testMetrics) JobSkip(name string) {
	t.skips.Add(1)
}

// ============================================================
// Scheduler
// ============================================================

const (
	defaultJobTimeout = 5 * time.Minute
	lockBuffer        = 10 * time.Second
)

// SchedulerOption is a functional option for NewScheduler.
type SchedulerOption func(*Scheduler)

// WithLocation sets the default timezone for NextRun computation.
// Default: time.UTC
func WithLocation(loc *time.Location) SchedulerOption {
	return func(s *Scheduler) { s.loc = loc }
}

// WithDefaultTimeout sets the default job timeout.
// Default: 5 minutes
func WithDefaultTimeout(d time.Duration) SchedulerOption {
	return func(s *Scheduler) { s.defaultJobTimeout = d }
}

// Scheduler is a cron engine with:
//   - Min-heap based scheduling
//   - Distributed locking (one job execution per cluster)
//   - Graceful shutdown (waits for running jobs)
//   - Panic recovery per job
//   - Pluggable metrics
type Scheduler struct {
	mu                sync.Mutex
	jobs              jobHeap
	wakeCh            chan struct{}
	locker            LockProvider
	metrics           Metrics
	loc               *time.Location
	defaultJobTimeout time.Duration

	wg     sync.WaitGroup
	ctx    context.Context
	cancel context.CancelFunc
}

// NewScheduler creates and starts a new scheduler.
// lock is required; metrics may be nil (uses noopMetrics).
func NewScheduler(lock LockProvider, m Metrics, opts ...SchedulerOption) *Scheduler {
	if m == nil {
		m = noopMetrics{}
	}

	ctx, cancel := context.WithCancel(context.Background())

	s := &Scheduler{
		wakeCh:            make(chan struct{}, 1),
		locker:            lock,
		metrics:           m,
		loc:               time.UTC,
		defaultJobTimeout: defaultJobTimeout,
		ctx:               ctx,
		cancel:            cancel,
	}

	for _, opt := range opts {
		opt(s)
	}

	heap.Init(&s.jobs)
	go s.loop()
	return s
}

// JobOption is a functional option for individual jobs.
type JobOption func(*Job)

// WithJobTimeout sets a custom timeout for a job,
// overriding the scheduler's defaultJobTimeout.
func WithJobTimeout(d time.Duration) JobOption {
	return func(j *Job) { j.Timeout = d }
}

// Cron registers a new job with the given cron expression.
// Returns an error if the spec is invalid or next run cannot be computed.
//
// Supported specs:
//   - "*/5 * * * *" → every 5 minutes
//   - "0 9 15W * *" → 9am on nearest weekday to 15th
//   - "0 0 L * *" → midnight on last day of month
//   - "0 0 L-3 * *" → midnight 3 days before month end
//   - "0 9 LW * *" → 9am on last weekday of month
//   - "0 8 * * 5L" → 8am on last Friday of month
//   - "0 10 * * 2#3" → 10am on 3rd Tuesday of month
func (s *Scheduler) Cron(name, spec string, fn func(ctx context.Context) error, opts ...JobOption) error {
	parser, err := ParseCron(spec)
	if err != nil {
		return fmt.Errorf("scheduler: parse spec for %q: %w", name, err)
	}

	now := time.Now().In(s.loc)
	nextRun, err := parser.Next(now, s.loc)
	if err != nil {
		return fmt.Errorf("scheduler: compute next run for %q: %w", name, err)
	}

	job := &Job{
		Name:    name,
		Cron:    parser,
		Run:     fn,
		Timeout: s.defaultJobTimeout,
		NextRun: nextRun,
	}

	for _, opt := range opts {
		opt(job)
	}

	s.Add(job)
	return nil
}

// Add adds a job directly to the scheduler without parsing.
func (s *Scheduler) Add(j *Job) {
	s.mu.Lock()
	heap.Push(&s.jobs, j)
	s.mu.Unlock()

	select {
	case s.wakeCh <- struct{}{}:
	default:
	}
}

// loop is the main scheduler goroutine.
func (s *Scheduler) loop() {
	var timer *time.Timer
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()

	for {
		s.mu.Lock()
		if len(s.jobs) == 0 {
			s.mu.Unlock()
			select {
			case <-s.wakeCh:
			case <-s.ctx.Done():
				return
			}
			continue
		}
		wait := time.Until(s.jobs[0].NextRun)
		s.mu.Unlock()

		if wait > 0 {
			if timer == nil {
				timer = time.NewTimer(wait)
			} else {
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(wait)
			}

			select {
			case <-timer.C:
			case <-s.wakeCh:
				continue
			case <-s.ctx.Done():
				return
			}
		}

		s.mu.Lock()
		if len(s.jobs) == 0 {
			s.mu.Unlock()
			continue
		}
		if time.Until(s.jobs[0].NextRun) > 0 {
			s.mu.Unlock()
			continue
		}

		job := heap.Pop(&s.jobs).(*Job)
		s.mu.Unlock()

		// Compute next run BEFORE spawning goroutine
		nextRun, err := job.Cron.Next(time.Now().In(s.loc), s.loc)
		if err != nil {
			log.Printf("[scheduler] job %q: cannot compute next run, removing: %v", job.Name, err)
		} else {
			job.NextRun = nextRun
			s.Add(job)
		}

		s.wg.Add(1)
		go s.execute(job)
	}
}

// execute runs a job with locking, timeout, and panic recovery.
func (s *Scheduler) execute(j *Job) {
	defer s.wg.Done()

	lockKey := "joblock:" + j.Name
	lockTTL := j.Timeout + lockBuffer

	ok, err := s.locker.TryLock(s.ctx, lockKey, lockTTL)
	if err != nil {
		log.Printf("[scheduler] job %q: lock error: %v", j.Name, err)
		s.metrics.JobSkip(j.Name)
		return
	}
	if !ok {
		s.metrics.JobSkip(j.Name)
		return
	}
	defer func() {
		if unlockErr := s.locker.Unlock(s.ctx, lockKey); unlockErr != nil {
			log.Printf("[scheduler] job %q: unlock error: %v", j.Name, unlockErr)
		}
	}()

	start := time.Now()
	s.metrics.JobStart(j.Name)

	runCtx, cancel := context.WithTimeout(s.ctx, j.Timeout)
	defer cancel()

	var runErr error
	func() {
		defer func() {
			if r := recover(); r != nil {
				runErr = fmt.Errorf("panic: %v", r)
				log.Printf("[scheduler] job %q panicked: %v", j.Name, r)
			}
		}()
		runErr = j.Run(runCtx)
	}()

	dur := time.Since(start)
	if runErr != nil {
		if errors.Is(runErr, context.DeadlineExceeded) {
			log.Printf("[scheduler] job %q timed out after %v", j.Name, j.Timeout)
		} else {
			log.Printf("[scheduler] job %q failed in %v: %v", j.Name, dur, runErr)
		}
		s.metrics.JobFail(j.Name, dur, runErr)
		return
	}

	s.metrics.JobSuccess(j.Name, dur)
	log.Printf("[scheduler] job %q succeeded in %v", j.Name, dur)
}

// Stop stops the scheduler and waits for all running jobs to complete.
func (s *Scheduler) Stop() {
	s.cancel()
	s.wg.Wait()
}

// ============================================================
// MemoryLock
// ============================================================

type memEntry struct {
	expires time.Time
}

// MemoryLock is an in-memory LockProvider for single-node deployments.
type MemoryLock struct {
	mu       sync.Mutex
	m        map[string]memEntry
	stop     chan struct{}
	gcPeriod time.Duration
}

func NewMemoryLock(d time.Duration) *MemoryLock {
	ml := &MemoryLock{
		m:        make(map[string]memEntry),
		stop:     make(chan struct{}),
		gcPeriod: d,
	}
	go ml.gcLoop()
	return ml
}

func (l *MemoryLock) gcLoop() {
	ticker := time.NewTicker(l.gcPeriod)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			now := time.Now()
			l.mu.Lock()
			for k, e := range l.m {
				if now.After(e.expires) {
					delete(l.m, k)
				}
			}
			l.mu.Unlock()
		case <-l.stop:
			return
		}
	}
}

func (l *MemoryLock) TryLock(_ context.Context, k string, ttl time.Duration) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if e, ok := l.m[k]; ok && time.Now().Before(e.expires) {
		return false, nil
	}

	l.m[k] = memEntry{expires: time.Now().Add(ttl)}
	return true, nil
}

func (l *MemoryLock) Unlock(_ context.Context, k string) error {
	l.mu.Lock()
	delete(l.m, k)
	l.mu.Unlock()
	return nil
}

func (l *MemoryLock) Stop() {
	close(l.stop)
}

// ============================================================
// RedisLock
// ============================================================

var luaUnlock = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
    return redis.call("DEL", KEYS[1])
end
return 0
`)

// RedisLock is a Redis-based LockProvider for multi-node deployments.
type RedisLock struct {
	rdb *redis.Client
	id  string
}

func NewRedisLock(rdb *redis.Client, instanceID string) *RedisLock {
	return &RedisLock{rdb: rdb, id: instanceID}
}

func (r *RedisLock) TryLock(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	return r.rdb.SetNX(ctx, key, r.id, ttl).Result() //nolint:staticcheck // atomic lock acquisition on supported Redis versions.
}

func (r *RedisLock) Unlock(ctx context.Context, key string) error {
	err := luaUnlock.Run(ctx, r.rdb, []string{key}, r.id).Err()
	if errors.Is(err, redis.Nil) {
		return nil
	}
	return err
}
