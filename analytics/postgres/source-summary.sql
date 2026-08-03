-- Run as the source schema owner. The result is inventory evidence only; it
-- never exposes row contents or sensitive columns.
SELECT current_database() AS database_name,
       current_setting('server_version') AS postgres_version,
       current_setting('wal_level') AS wal_level,
       current_setting('max_replication_slots') AS max_replication_slots,
       current_setting('max_wal_senders') AS max_wal_senders;

SELECT slot_name,
       slot_type,
       plugin,
       active,
       pg_size_pretty(pg_wal_lsn_diff(pg_current_wal_lsn(), restart_lsn)) AS retained_wal
FROM pg_replication_slots
WHERE slot_name LIKE 'seev_analytics_%'
ORDER BY slot_name;
