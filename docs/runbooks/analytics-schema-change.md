# Analytics schema change

Symptoms: Connect schema-history error, prohibited-column detection, or dbt
`on_schema_change=fail`. Additive nullable changes may be reviewed; drops,
renames, narrowing, money-unit, timestamp-semantic, key, or enum changes block.

```bash
make analytics-config-check
make analytics-connectors-status
```

Pause the affected connector if the change is not approved. Compare the source
migration to the contract and confirm no sensitive field entered a topic. Keep
the previous marts available where safe, update the owner contract and fixture,
and re-snapshot/rebuild only after review. Reconcile before resume. Record the
schema fingerprint and classification; do not silently default a missing
financial field.
