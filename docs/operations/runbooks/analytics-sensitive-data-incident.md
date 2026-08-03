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
