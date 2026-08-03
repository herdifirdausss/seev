CREATE VIEW IF NOT EXISTS staging.ledger_accounts_changes AS
SELECT
    toUUIDOrZero(JSONExtractString(payload, 'id')) AS id,
    JSONExtractString(payload, 'owner_type') AS owner_type,
    JSONExtractString(payload, 'type') AS account_type,
    upper(JSONExtractString(payload, 'currency')) AS currency,
    nullIf(JSONExtractString(payload, 'pocket_code'), '') AS pocket_code,
    nullIf(JSONExtractString(payload, 'system_qualifier'), '') AS system_qualifier,
    JSONExtractString(payload, 'status') AS status,
    parseDateTime64BestEffortOrNull(JSONExtractString(payload, 'created_at'), 6, 'UTC') AS created_at_utc,
    parseDateTime64BestEffortOrNull(JSONExtractString(payload, 'updated_at'), 6, 'UTC') AS updated_at_utc,
    operation,
    operation = 'd' OR JSONExtractBool(payload, '__deleted') AS is_deleted,
    source_lsn,
    partition,
    offset,
    source_timestamp,
    ingested_at
FROM raw.cdc_events_deduplicated
WHERE source_service = 'ledger' AND source_table = 'accounts';

CREATE VIEW IF NOT EXISTS staging.ledger_accounts_current AS
SELECT *
FROM
(
    SELECT *
    FROM staging.ledger_accounts_changes
    ORDER BY id, coalesce(source_lsn, 0) DESC, partition DESC, offset DESC
    LIMIT 1 BY id
)
WHERE NOT is_deleted;

CREATE VIEW IF NOT EXISTS staging.ledger_balances_changes AS
SELECT
    toUUIDOrZero(JSONExtractString(payload, 'account_id')) AS account_id,
    toInt64(JSONExtractInt(payload, 'balance')) AS balance_minor,
    JSONExtractBool(payload, 'allow_negative') AS allow_negative,
    parseDateTime64BestEffortOrNull(JSONExtractString(payload, 'updated_at'), 6, 'UTC') AS updated_at_utc,
    operation,
    operation = 'd' OR JSONExtractBool(payload, '__deleted') AS is_deleted,
    source_lsn,
    partition,
    offset,
    source_timestamp,
    ingested_at
FROM raw.cdc_events_deduplicated
WHERE source_service = 'ledger' AND source_table = 'account_balances';

CREATE VIEW IF NOT EXISTS staging.ledger_balances_current AS
SELECT *
FROM
(
    SELECT *
    FROM staging.ledger_balances_changes
    ORDER BY account_id, coalesce(source_lsn, 0) DESC, partition DESC, offset DESC
    LIMIT 1 BY account_id
)
WHERE NOT is_deleted;

CREATE VIEW IF NOT EXISTS staging.ledger_transactions_changes AS
SELECT
    toUUIDOrZero(JSONExtractString(payload, 'id')) AS id,
    JSONExtractString(payload, 'type') AS transaction_type,
    JSONExtractString(payload, 'status') AS status,
    toInt64(JSONExtractInt(payload, 'amount')) AS amount_minor,
    upper(JSONExtractString(payload, 'currency')) AS currency,
    toUUIDOrZero(JSONExtractString(payload, 'source_account_id')) AS source_account_id,
    toUUIDOrZero(JSONExtractString(payload, 'destination_account_id')) AS destination_account_id,
    nullIf(JSONExtractString(payload, 'external_ref'), '') AS external_ref,
    nullIf(JSONExtractString(payload, 'gateway'), '') AS gateway,
    nullIf(JSONExtractString(payload, 'request_id'), '') AS request_id,
    toUUIDOrZero(JSONExtractString(payload, 'closed_by_tx_id')) AS closed_by_tx_id,
    nullIf(JSONExtractString(payload, 'closed_reason'), '') AS closed_reason,
    parseDateTime64BestEffortOrNull(JSONExtractString(payload, 'created_at'), 6, 'UTC') AS created_at_utc,
    parseDateTime64BestEffortOrNull(JSONExtractString(payload, 'updated_at'), 6, 'UTC') AS updated_at_utc,
    operation,
    operation = 'd' OR JSONExtractBool(payload, '__deleted') AS is_deleted,
    source_lsn,
    partition,
    offset,
    source_timestamp,
    ingested_at
FROM raw.cdc_events_deduplicated
WHERE source_service = 'ledger' AND source_table = 'ledger_transactions';

CREATE VIEW IF NOT EXISTS staging.ledger_transactions_current AS
SELECT *
FROM
(
    SELECT *
    FROM staging.ledger_transactions_changes
    ORDER BY id, coalesce(source_lsn, 0) DESC, partition DESC, offset DESC
    LIMIT 1 BY id
)
WHERE NOT is_deleted;

CREATE VIEW IF NOT EXISTS staging.ledger_entries_changes AS
SELECT
    toUUIDOrZero(JSONExtractString(payload, 'id')) AS id,
    toUUIDOrZero(JSONExtractString(payload, 'transaction_id')) AS transaction_id,
    toUUIDOrZero(JSONExtractString(payload, 'account_id')) AS account_id,
    JSONExtractString(payload, 'direction') AS direction,
    toInt64(JSONExtractInt(payload, 'amount')) AS amount_minor,
    toInt64(JSONExtractInt(payload, 'balance_after')) AS balance_after_minor,
    parseDateTime64BestEffortOrNull(JSONExtractString(payload, 'created_at'), 6, 'UTC') AS created_at_utc,
    operation,
    operation = 'd' OR JSONExtractBool(payload, '__deleted') AS is_deleted,
    source_lsn,
    partition,
    offset,
    source_timestamp,
    ingested_at
FROM raw.cdc_events_deduplicated
WHERE source_service = 'ledger' AND source_table = 'ledger_entries';

CREATE VIEW IF NOT EXISTS staging.ledger_fee_quotes_changes AS
SELECT
    toUUIDOrZero(JSONExtractString(payload, 'id')) AS id,
    nullIf(JSONExtractString(payload, 'user_pseudonym'), '') AS user_pseudonym,
    JSONExtractString(payload, 'transaction_type') AS transaction_type,
    JSONExtractString(payload, 'gateway') AS gateway,
    upper(JSONExtractString(payload, 'currency')) AS currency,
    toInt64(JSONExtractInt(payload, 'amount')) AS amount_minor,
    toInt64(JSONExtractInt(payload, 'fee_amount')) AS quoted_fee_minor,
    JSONExtractString(payload, 'fee_gateway') AS fee_gateway,
    parseDateTime64BestEffortOrNull(JSONExtractString(payload, 'expires_at'), 6, 'UTC') AS expires_at_utc,
    parseDateTime64BestEffortOrNull(JSONExtractString(payload, 'consumed_at'), 6, 'UTC') AS consumed_at_utc,
    nullIf(JSONExtractString(payload, 'consumed_by_ref'), '') AS consumed_by_ref,
    parseDateTime64BestEffortOrNull(JSONExtractString(payload, 'created_at'), 6, 'UTC') AS created_at_utc,
    operation,
    operation = 'd' OR JSONExtractBool(payload, '__deleted') AS is_deleted,
    source_lsn,
    partition,
    offset,
    source_timestamp,
    ingested_at
FROM raw.cdc_events_deduplicated
WHERE source_service = 'ledger' AND source_table = 'fee_quotes';

CREATE VIEW IF NOT EXISTS staging.ledger_fee_quotes_current AS
SELECT *
FROM
(
    SELECT *
    FROM staging.ledger_fee_quotes_changes
    ORDER BY id, coalesce(source_lsn, 0) DESC, partition DESC, offset DESC
    LIMIT 1 BY id
)
WHERE NOT is_deleted;

CREATE VIEW IF NOT EXISTS staging.payin_intents_changes AS
SELECT
    toUUIDOrZero(JSONExtractString(payload, 'id')) AS id,
    JSONExtractString(payload, 'reference') AS reference,
    nullIf(JSONExtractString(payload, 'user_pseudonym'), '') AS user_pseudonym,
    toInt64(JSONExtractInt(payload, 'amount')) AS amount_minor,
    upper(JSONExtractString(payload, 'currency')) AS currency,
    JSONExtractString(payload, 'vendor') AS vendor,
    JSONExtractString(payload, 'status') AS status,
    toUUIDOrZero(JSONExtractString(payload, 'settled_event_id')) AS settled_event_id,
    parseDateTime64BestEffortOrNull(JSONExtractString(payload, 'expires_at'), 6, 'UTC') AS expires_at_utc,
    nullIf(JSONExtractString(payload, 'request_id'), '') AS request_id,
    toUUIDOrZero(JSONExtractString(payload, 'merchant_tenant_id')) AS merchant_tenant_id,
    nullIf(JSONExtractString(payload, 'downstream_key'), '') AS downstream_key,
    parseDateTime64BestEffortOrNull(JSONExtractString(payload, 'created_at'), 6, 'UTC') AS created_at_utc,
    parseDateTime64BestEffortOrNull(JSONExtractString(payload, 'updated_at'), 6, 'UTC') AS updated_at_utc,
    operation,
    operation = 'd' OR JSONExtractBool(payload, '__deleted') AS is_deleted,
    source_lsn,
    partition,
    offset,
    source_timestamp,
    ingested_at
FROM raw.cdc_events_deduplicated
WHERE source_service = 'payin' AND source_table = 'payin_topup_intents';

CREATE VIEW IF NOT EXISTS staging.payin_intents_current AS
SELECT *
FROM
(
    SELECT *
    FROM staging.payin_intents_changes
    ORDER BY id, coalesce(source_lsn, 0) DESC, partition DESC, offset DESC
    LIMIT 1 BY id
)
WHERE NOT is_deleted;

CREATE VIEW IF NOT EXISTS staging.payin_webhooks_changes AS
SELECT
    toUUIDOrZero(JSONExtractString(payload, 'id')) AS id,
    JSONExtractString(payload, 'vendor') AS vendor,
    JSONExtractString(payload, 'vendor_event_id') AS vendor_event_id,
    JSONExtractString(payload, 'external_ref') AS external_ref,
    nullIf(JSONExtractString(payload, 'user_pseudonym'), '') AS user_pseudonym,
    toInt64(JSONExtractInt(payload, 'amount')) AS amount_minor,
    upper(JSONExtractString(payload, 'currency')) AS currency,
    JSONExtractString(payload, 'status') AS status,
    nullIf(JSONExtractString(payload, 'request_id'), '') AS request_id,
    toUUIDOrZero(JSONExtractString(payload, 'merchant_tenant_id')) AS merchant_tenant_id,
    parseDateTime64BestEffortOrNull(JSONExtractString(payload, 'created_at'), 6, 'UTC') AS created_at_utc,
    parseDateTime64BestEffortOrNull(JSONExtractString(payload, 'updated_at'), 6, 'UTC') AS updated_at_utc,
    operation,
    operation = 'd' OR JSONExtractBool(payload, '__deleted') AS is_deleted,
    source_lsn,
    partition,
    offset,
    source_timestamp,
    ingested_at
FROM raw.cdc_events_deduplicated
WHERE source_service = 'payin' AND source_table = 'payin_webhook_events';

CREATE VIEW IF NOT EXISTS staging.payout_requests_changes AS
SELECT
    toUUIDOrZero(JSONExtractString(payload, 'id')) AS id,
    nullIf(JSONExtractString(payload, 'user_pseudonym'), '') AS user_pseudonym,
    toInt64(JSONExtractInt(payload, 'amount')) AS amount_minor,
    upper(JSONExtractString(payload, 'currency')) AS currency,
    JSONExtractString(payload, 'vendor') AS vendor,
    JSONExtractString(payload, 'status') AS status,
    toUUIDOrZero(JSONExtractString(payload, 'hold_tx_id')) AS hold_tx_id,
    toUUIDOrZero(JSONExtractString(payload, 'settle_tx_id')) AS settle_tx_id,
    nullIf(JSONExtractString(payload, 'vendor_ref'), '') AS vendor_ref,
    toUUIDOrZero(JSONExtractString(payload, 'fee_quote_id')) AS fee_quote_id,
    toInt64OrNull(JSONExtractString(payload, 'fee_amount')) AS fee_amount_minor,
    nullIf(JSONExtractString(payload, 'fee_gateway'), '') AS fee_gateway,
    nullIf(JSONExtractString(payload, 'request_id'), '') AS request_id,
    toUUIDOrZero(JSONExtractString(payload, 'merchant_tenant_id')) AS merchant_tenant_id,
    nullIf(JSONExtractString(payload, 'downstream_key'), '') AS downstream_key,
    parseDateTime64BestEffortOrNull(JSONExtractString(payload, 'created_at'), 6, 'UTC') AS created_at_utc,
    parseDateTime64BestEffortOrNull(JSONExtractString(payload, 'updated_at'), 6, 'UTC') AS updated_at_utc,
    operation,
    operation = 'd' OR JSONExtractBool(payload, '__deleted') AS is_deleted,
    source_lsn,
    partition,
    offset,
    source_timestamp,
    ingested_at
FROM raw.cdc_events_deduplicated
WHERE source_service = 'payout' AND source_table = 'payout_requests';

CREATE VIEW IF NOT EXISTS staging.payout_requests_current AS
SELECT *
FROM
(
    SELECT *
    FROM staging.payout_requests_changes
    ORDER BY id, coalesce(source_lsn, 0) DESC, partition DESC, offset DESC
    LIMIT 1 BY id
)
WHERE NOT is_deleted;

CREATE VIEW IF NOT EXISTS staging.payout_calls_changes AS
SELECT
    toUUIDOrZero(JSONExtractString(payload, 'id')) AS id,
    toUUIDOrZero(JSONExtractString(payload, 'payout_request_id')) AS payout_request_id,
    JSONExtractString(payload, 'vendor') AS vendor,
    JSONExtractInt(payload, 'attempt') AS attempt,
    nullIf(JSONExtractString(payload, 'resp_status'), '') AS response_status,
    JSONExtractString(payload, 'outcome') AS outcome,
    parseDateTime64BestEffortOrNull(JSONExtractString(payload, 'created_at'), 6, 'UTC') AS created_at_utc,
    operation,
    operation = 'd' OR JSONExtractBool(payload, '__deleted') AS is_deleted,
    source_lsn,
    partition,
    offset,
    source_timestamp,
    ingested_at
FROM raw.cdc_events_deduplicated
WHERE source_service = 'payout' AND source_table = 'payout_vendor_calls';
