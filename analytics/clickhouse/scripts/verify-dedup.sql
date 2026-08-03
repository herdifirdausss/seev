-- Duplicate transport identity must be zero in the deduplicated projection.
SELECT topic, partition, offset, count()
FROM raw.cdc_events_deduplicated
GROUP BY topic, partition, offset
HAVING count() != 1;

-- Immutable Ledger entries are allowed only one logical row per primary key.
SELECT id, count()
FROM staging.ledger_entries_changes
WHERE NOT is_deleted
GROUP BY id
HAVING count() > 1;
