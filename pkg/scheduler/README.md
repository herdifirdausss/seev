# Cron Scheduler

pkg/scheduler provides the shared in-process cron engine used by ledger and
payout workers. It supports calendar extensions, single-node or Redis-backed
locking, per-job timeouts, graceful shutdown, panic isolation, and metrics.

## Supported expressions

Expressions use five fields:

~~~text
minute hour day-of-month month day-of-week
~~~

Standard values, lists, ranges, and steps are supported. Calendar extensions:

| Syntax | Meaning |
|---|---|
| L | Last day of the month |
| L-n | n days before the end of the month |
| nW | Weekday nearest to day n |
| LW | Last weekday of the month |
| nL | Last occurrence of weekday n |
| n#m | The mth occurrence of weekday n |

Examples:

~~~text
*/5 * * * *     every five minutes
0 9 15W * *     09:00 on the weekday nearest the 15th
0 0 L * *       midnight on the final day of the month
0 8 * * 5L      08:00 on the final Friday
0 10 * * 2#3    10:00 on the third Tuesday
~~~

When both day-of-month and day-of-week are restricted, matching follows
Vixie-cron OR semantics.

## Basic usage

~~~go
package example

import (
    "context"
    "time"

    "github.com/herdifirdausss/seev/pkg/scheduler"
)

func start() error {
    lock := scheduler.NewMemoryLock(time.Minute)
    defer lock.Stop()

    sched := scheduler.NewScheduler(
        lock,
        nil,
        scheduler.WithLocation(time.UTC),
        scheduler.WithDefaultTimeout(5*time.Minute),
    )
    defer sched.Stop()

    return sched.Cron(
        "reconciliation",
        "0 2 * * *",
        func(ctx context.Context) error {
            return runReconciliation(ctx)
        },
        scheduler.WithJobTimeout(2*time.Minute),
    )
}
~~~

Cron returns an error for invalid expressions or when it cannot calculate a
next run. Handle that error during service startup.

## Lock providers

### Memory lock

NewMemoryLock is appropriate for one process. The duration argument controls
cleanup frequency for expired lock entries. Call Stop during shutdown.

### Redis lock

Use NewRedisLock when multiple replicas must coordinate the same job:

~~~go
lock := scheduler.NewRedisLock(redisClient, instanceID)
sched := scheduler.NewScheduler(lock, scheduler.NewPrometheusMetrics())
~~~

The Redis implementation acquires locks with SET NX and releases them through
an ownership-checking Lua script. Each replica must use a stable, unique
instance identifier.

If lock acquisition returns an error or reports that another replica owns the
lock, the current tick is skipped. The scheduler does not run the job without a
lock.

## Execution behavior

- Jobs are ordered by next-run time in a min-heap.
- The next occurrence is scheduled before the current occurrence starts.
- A context deadline is created from the job timeout.
- Panics are recovered and reported as job failures.
- Stop prevents new work and waits for running jobs.
- Lock TTL is the job timeout plus a safety buffer.
- Impossible schedules are bounded to approximately four years of search.

Jobs must still cooperate with context cancellation. A timeout cannot forcibly
stop code that ignores its context.

## Metrics

The Metrics interface reports starts, successes, failures, and skipped ticks.
Passing nil enables no-op metrics. NewPrometheusMetrics currently records the
skip counter used by repository workers:

~~~text
scheduler_job_skips_total{job="..."}
~~~

Keep job names static to avoid unbounded metric cardinality.

## Verification

~~~bash
go test -race ./pkg/scheduler
go test -run '^$' -bench . ./pkg/scheduler
~~~

Tests cover parsing, calendar boundaries, concurrency, lock ownership, timeout,
panic recovery, shutdown, and benchmarks. Redis lock tests use an in-process
Redis-compatible test server.

## Source files

~~~text
pkg/scheduler/
├── scheduler.go
├── scheduler_test.go
├── scheduler_unit_test.go
├── metrics.go
└── README.md
~~~

This package does not define the repository's licensing terms. Refer to a
repository-level license file if one is added.
