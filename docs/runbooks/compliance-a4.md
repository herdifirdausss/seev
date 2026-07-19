# Compliance A4 runbook

## KYC apply retry dead-letter

Alert `SeevKYCApplyRetryDead` means ledger policy limits could not be applied
after the bounded retry budget. The submission remains pending; do not edit
`auth_users` or `policy_limits` manually. Check the ledger gRPC health and
`auth_kyc_apply_attempts_total`, then re-run the auth relay after recovery. A
manual approve is safe and idempotent, but approval must still pass
`ApplyKycTier` before the level changes.

## Screening spill

`fraud_screening_event_spill_depth` is a bounded in-memory queue. Restore fraud
Postgres first; the flusher preserves FIFO order. `fraud_screening_events_lost_total`
is an accepted, measured loss boundary for a process crash while spill is
non-empty. Preserve the metric/log evidence and investigate the outage; never
reconstruct or delete screening events by hand.

## Sanctions dataset

Use `go run ./cmd/sanctions-loader -file <jsonl> -version <version>` with a
verified offline export. The loader replaces the local subset transactionally.
The source documentation describes the latest metadata/index and bulk export
workflow; record the version and checksum in the change ticket.

## KYC documents

Without a configured object-store adapter or 32-byte `KYC_DOC_KEK`, uploads
return `503 DOCUMENT_STORAGE_UNAVAILABLE`; this is intentional. Never log KEK,
plaintext bytes, or object contents. Download is internal admin-only and
decrypts in memory.
