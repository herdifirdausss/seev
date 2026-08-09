# C2 data-platform threat model

| Threat | Control | Test/evidence owner |
| --- | --- | --- |
| Replication credential writes to OLTP | dedicated `LOGIN REPLICATION` role; explicit SELECT grants; no app role | T2 source setup |
| Wildcard CDC captures a secret | table/column allowlist and contract validator | analytics contract validator |
| User identity enters a topic | HMAC pseudonymization SMT with required secret file | SMT fixture and sensitive scan |
| Raw callback/destination data leaks | fields absent from connector allowlists and warehouse models | connector fixture/scan |
| Metabase reads raw or writes ClickHouse | `bi_readonly` grants only mart/dimensions; no DML privilege | ClickHouse role check |
| Public exposure | all host ports bind `127.0.0.1`; internal analytics network only | Compose config evidence |
| Connector config leaks password | credentials substituted in memory; diagnostic output prints state only | connector script review |
| WAL fills source disk | bounded slot retention and warning/critical alerts; source-protection runbook | WAL drill |
| Duplicate CDC doubles money | transport dedup plus deterministic latest-state/core keys | duplicate fixture |
| Stale data is shown as current | freshness columns, banner, reconciliation status | dashboard catalog |
| Malicious/incompatible schema change | connector fails closed, dbt `on_schema_change=fail`, runbook | schema fixture |
| Analytics outage blocks money movement | no product dependency or analytics credential in service config | OLTP outage journey |

The pseudonym salt, source passwords, ClickHouse passwords, and Metabase local
state are local-only secrets. They are ignored by Git and startup fails when a
required secret file is absent. Analytics may be reset or re-snapshotted to
protect the source; no source financial row is rolled back by C2.
