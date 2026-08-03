# Analytics data contracts

The versioned contract files are:

- [source allowlist](../../analytics/contracts/sources.yaml)
- [privacy policy](../../analytics/contracts/privacy.yaml)
- [topic contract](../../analytics/contracts/topics.yaml)
- [model contract](../../analytics/contracts/models.yaml)
- [metric contract](../../analytics/contracts/metrics.yaml)
- [correlation matrix](../../analytics/contracts/correlation-matrix.md)

The validator rejects wildcard capture, missing ownership/purpose metadata,
unstable keys, unsupported transformations, and connector table/column lists
that do not match the reviewed manifest. Connect configuration is declarative;
runtime credentials are substituted only by the apply script.

## Change policy

Compatible additive nullable fields may be reviewed and added to the allowlist.
Drops, renames, narrowing, money-unit changes, timestamp-semantic changes,
primary-key changes, and enum meaning changes fail visibly and require a new
contract version. A source schema change never silently changes a dashboard's
business meaning.

## At-least-once identity

Transport identity is `(topic, partition, offset)`. Logical current-state
identity is `(source service, table, primary key)` ordered by source LSN, then
partition and offset. Delete markers remain observable; mutable current models
exclude the latest deleted state. A delete on immutable Ledger entries is a
critical reconciliation failure.
