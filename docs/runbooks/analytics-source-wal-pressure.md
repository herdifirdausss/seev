# Analytics source WAL pressure

Symptoms: retained WAL alert, source disk growth, or `pg_replication_slots`
shows an inactive C2 slot. Source safety is critical; dashboards may be lost.

```bash
psql "$SOURCE_ADMIN_DSN" -f analytics/postgres/source-summary.sql
make analytics-connectors-pause
```

Stop analytics consumers and protect the source. If the bound is still unsafe,
use the explicitly confirmed `analytics/postgres/drop-replication.sh` procedure
after recording the slot LSN. This loses analytical continuity, not source
money. Recreate the slot/publication, perform a fresh approved snapshot, and
reconcile before exposing dashboards. Record before/after retained bytes and
source disk usage.
