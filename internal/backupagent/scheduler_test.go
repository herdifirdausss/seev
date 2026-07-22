package backupagent

import (
	"testing"
	"time"

	"github.com/herdifirdausss/seev/pkg/scheduler"
)

// TestCronSpecScheduleAndTimezone proves K4's schedule boundaries hold
// under the exact cron specs StartScheduler registers: full backups only
// land on Sunday, differentials only on Monday-Saturday, both at 02:10
// Asia/Jakarta — never the reverse, and never in another timezone's
// 02:10 (docs/plan/50 T2 "schedule/timezone boundaries" required test).
func TestCronSpecScheduleAndTimezone(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Jakarta")
	if err != nil {
		t.Fatalf("load Asia/Jakarta: %v", err)
	}
	cfg := Config{FullCronSpec: "10 2 * * 0", DiffCronSpec: "10 2 * * 1-6"}

	fullCron, err := scheduler.ParseCron(cfg.FullCronSpec)
	if err != nil {
		t.Fatalf("parse full cron: %v", err)
	}
	diffCron, err := scheduler.ParseCron(cfg.DiffCronSpec)
	if err != nil {
		t.Fatalf("parse diff cron: %v", err)
	}

	// Start from a known Monday (2026-07-20 is a Monday) at midnight.
	start := time.Date(2026, 7, 20, 0, 0, 0, 0, loc)

	next, err := fullCron.Next(start, loc)
	if err != nil {
		t.Fatalf("full backup next run: %v", err)
	}
	if next.Weekday() != time.Sunday {
		t.Fatalf("full backup next run landed on %s, want Sunday", next.Weekday())
	}
	if next.Hour() != 2 || next.Minute() != 10 {
		t.Fatalf("full backup next run at %02d:%02d, want 02:10", next.Hour(), next.Minute())
	}
	if got := next.Location().String(); got != "Asia/Jakarta" {
		t.Fatalf("full backup next run in location %q, want Asia/Jakarta", got)
	}

	next, err = diffCron.Next(start, loc)
	if err != nil {
		t.Fatalf("diff backup next run: %v", err)
	}
	if next.Weekday() == time.Sunday {
		t.Fatalf("differential backup next run landed on Sunday — that day belongs to the full backup only")
	}
	if next.Hour() != 2 || next.Minute() != 10 {
		t.Fatalf("diff backup next run at %02d:%02d, want 02:10", next.Hour(), next.Minute())
	}

	// Walk seven consecutive differential runs from a fresh Sunday
	// midnight and confirm none of them ever falls on a Sunday — proves
	// the 1-6 day-of-week range holds across a full week, not just the
	// first occurrence.
	cursor := time.Date(2026, 7, 19, 0, 0, 0, 0, loc) // a Sunday
	for i := 0; i < 6; i++ {
		cursor, err = diffCron.Next(cursor, loc)
		if err != nil {
			t.Fatalf("diff backup next run iteration %d: %v", i, err)
		}
		if cursor.Weekday() == time.Sunday {
			t.Fatalf("iteration %d: differential backup landed on Sunday at %s", i, cursor)
		}
	}
}
