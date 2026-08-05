# Analytics sensitive-data incident

Symptoms: prohibited field appears in a connector config/topic/raw table,
secret is printed, or a dashboard exposes an identity/destination. Treat the
analytical copy as disposable; source safety and containment come first.

```bash
make analytics-connectors-pause
make analytics-verify
```

Stop downstream exposure, preserve only redacted evidence, rotate the affected
secret/salt, remove the analytical topic/warehouse data through the disposable
reset path, and re-snapshot from the reviewed allowlist. Do not edit or delete
source financial rows. Review exports/Metabase permissions and run the privacy
scan before re-enabling. Record field name/classification, exposure window,
affected topic/model, rotation, purge, and reviewer.

Confirmed 2026-08-05, a real incident, not a hypothetical: the
`PseudonymizeField` SMT was a silent no-op because it checked for `Map`
values, but Kafka Connect's SMT chain passes typed `Struct`/`Schema` records
at that pipeline stage — the transform never matched, `user_id` reached
`raw.cdc_events.payload` unpseudonymized, and the connector still reported
`RUNNING` with no error. Verify with a live streaming event, not just a
snapshot: `UPDATE fee_quotes SET id = id WHERE id = '<id>'`, then confirm
the resulting row shows `user_pseudonym`, not `user_id`. Also: `make
analytics-verify`'s static scan had its own silent-no-op bug (`rg` not
installed → the `if` fell through as "no match") — confirm `grep` is what
actually runs, not `rg`, before trusting a clean scan result on a new host.
