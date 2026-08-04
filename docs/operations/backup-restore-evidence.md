# Backup and restore evidence record

Use this template for every scheduled or release-blocking restore drill. The
record is incomplete until the operator attaches command output and checksums
from the isolated restore target.

| Field | Value |
|---|---|
| Drill ID / date / operator |  |
| Source backup and database snapshot |  |
| Target environment | isolated, never the live writer |
| Backup age at start |  |
| Restore start/end and elapsed time |  |
| RPO observed |  |
| RTO observed |  |
| Checksum/manifest result | pass / fail |
| Ledger trial balance result | pass / fail |
| Cross-service reconciliation result | pass / fail |
| Application smoke/golden route | pass / fail |
| Data-loss or anomaly ticket |  |
| Approver and follow-up owner |  |

The drill must not overwrite a live database. Use the existing backup and
`operations/recovery/drverify` tooling, retain logs without credentials, and update the
production-readiness scorecard with the measured RPO/RTO.
