# Reconciliation runner

`cmd/reconcile` is a bounded operational CLI, not an application service. It
opens the Ledger connection read-only, sets a statement timeout, compares
integer summaries at a safe cutoff, persists redacted results to ClickHouse
control tables, and exits non-zero for critical money mismatches.

It never repairs source rows, accepts arbitrary SQL, or emits source row
details. The control run is independently rerunnable with a new run id.
