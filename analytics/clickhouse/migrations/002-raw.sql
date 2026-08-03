CREATE TABLE IF NOT EXISTS raw.cdc_events
(
    topic String,
    partition Int32,
    offset Int64,
    message_key String,
    payload String,
    source_service LowCardinality(String),
    source_schema LowCardinality(String),
    source_table LowCardinality(String),
    operation LowCardinality(String),
    source_lsn Nullable(UInt64),
    source_tx_id Nullable(String),
    source_timestamp DateTime64(6, 'UTC'),
    connector_timestamp DateTime64(6, 'UTC'),
    ingested_at DateTime64(6, 'UTC'),
    event_date Date MATERIALIZED toDate(ingested_at)
)
ENGINE = MergeTree
PARTITION BY toYYYYMM(event_date)
ORDER BY (source_service, source_table, topic, partition, offset)
TTL ingested_at + INTERVAL 30 DAY DELETE;

CREATE TABLE IF NOT EXISTS raw.cdc_events_kafka
(
    payload String
)
ENGINE = Kafka
SETTINGS
    kafka_broker_list = 'redpanda:9092',
    kafka_topic_list = 'seev.cdc.ledger.public.accounts.v1,seev.cdc.ledger.public.account_balances.v1,seev.cdc.ledger.public.ledger_transactions.v1,seev.cdc.ledger.public.ledger_entries.v1,seev.cdc.ledger.public.fee_quotes.v1,seev.cdc.payin.public.payin_topup_intents.v1,seev.cdc.payin.public.payin_webhook_events.v1,seev.cdc.payout.public.payout_requests.v1,seev.cdc.payout.public.payout_vendor_calls.v1',
    kafka_group_name = 'seev-clickhouse-raw-v1',
    kafka_format = 'JSONAsString',
    kafka_num_consumers = 1,
    kafka_thread_per_consumer = 1;

CREATE MATERIALIZED VIEW IF NOT EXISTS raw.mv_cdc_events_kafka
TO raw.cdc_events
AS
SELECT
    _topic AS topic,
    _partition AS partition,
    _offset AS offset,
    coalesce(_key, '') AS message_key,
    payload,
    arrayElement(splitByChar('.', _topic), 3) AS source_service,
    JSONExtractString(payload, '__schema') AS source_schema,
    JSONExtractString(payload, '__table') AS source_table,
    JSONExtractString(payload, '__op') AS operation,
    nullIf(JSONExtractUInt(payload, '__lsn'), 0) AS source_lsn,
    nullIf(JSONExtractString(payload, '__txId'), '') AS source_tx_id,
    fromUnixTimestamp64Milli(toInt64(JSONExtractUInt(payload, '__source_ts_ms'))) AS source_timestamp,
    now64(6, 'UTC') AS connector_timestamp,
    now64(6, 'UTC') AS ingested_at
FROM raw.cdc_events_kafka;

CREATE VIEW IF NOT EXISTS raw.cdc_events_deduplicated AS
SELECT *
FROM raw.cdc_events
ORDER BY topic, partition, offset
LIMIT 1 BY topic, partition, offset;
