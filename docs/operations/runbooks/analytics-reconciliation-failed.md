# Analytics reconciliation failed

Symptoms: control run status is failed/stale, a critical money delta appears,
or a debit-credit invariant fails. Impact is analytical trust, not transaction
execution.

```bash
make analytics-reconcile
```

Confirm the safe cutoff, connector offsets, source summary, duplicate transport
rows, and dbt invocation. A critical mismatch exits non-zero and is persisted;
it must not be repaired by writing source data. Rerun with a new run id after
catch-up, classify unlinked legacy Payin rows explicitly, and hide/label
affected dashboards until the result passes. Record expected/actual integer
values, delta, currency, cutoff, and no sensitive row detail.
