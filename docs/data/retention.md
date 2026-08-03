# Data Retention Matrix

> [Documentation home](../../README.md) · [Data](README.md)

> **Generated from [config/data-retention.yaml](../../config/data-retention.yaml) — do not hand-edit this file.** Regenerate with `make retention-docs` after changing the policy. `cmd/retentioncheck` fails CI if this file and the policy ever disagree.

Policy version: **1**. See [docs/roadmap/archive/51-a8-data-lifecycle-privacy.md](../roadmap/archive/51-a8-data-lifecycle-privacy.md) for the locked design decisions (K1–K13) this matrix implements.

These are conservative engineering defaults for this learning repository, not an approved jurisdiction/product policy — see that document's §3 "Out of scope."

## Permanent tables

No entry may fully delete a row from these tables — only `retain_permanent`, `retain_immutable`, `retain_state`, or `redact` (a specific non-financial column, row kept) are allowed:

- `ledger.account_balance_snapshots`
- `ledger.account_balances`
- `ledger.accounts`
- `ledger.ledger_entries`
- `ledger.ledger_transactions`
- `ledger.pending_adjustments`
- `payin.payin_topup_intents`
- `payout.payout_requests`
- `payout.payout_vendor_calls`

## adminbff

| Class | Table / object | Classification | Terminal timestamp | Duration | Action | Batch | Hold scope |
|---|---|---|---|---|---|---|---|
| `adminbff.audit_log` | adminbff.audit_log | personal | created_at | 365d | delete | 500 | subject |
| `adminbff.retention_audit` | adminbff.adminbff_retention_audit | internal | — | — | retain_permanent | — | none |
| `adminbff.retention_holds` | adminbff.adminbff_retention_holds | internal | — | — | retain_state | — | none |
| `adminbff.sessions` | adminbff.sessions | personal | GREATEST(expires_at, absolute_expires_at) | 7d | delete | 500 | subject |

**`adminbff.audit_log`** — docs/roadmap/archive/51 §4.2 'Admin audit row'. Never eligible while a hold applies.

**`adminbff.retention_audit`** — docs/roadmap/archive/51 K4: the append-only proof that every purge/redact ran correctly. Purging this table would defeat its own purpose — never age-purged.

**`adminbff.retention_holds`** — docs/roadmap/archive/51 K5. A hold's own lifecycle (create/release) is managed by the closure/hold API, not a generic age rule — released holds are not itemized for independent purge in the locked matrix.

**`adminbff.sessions`** — Matches docs/roadmap/archive/51 §4.2 "Expired admin session". The current retention runner invokes fn_retention_purge_sessions every five minutes; the database function deletes sessions only after the effective expiry is older than seven days.

## assurance

| Class | Table / object | Classification | Terminal timestamp | Duration | Action | Batch | Hold scope |
|---|---|---|---|---|---|---|---|
| `assurance.alert_deliveries` | assurance.assurance_alert_deliveries | internal | COALESCE(delivered_at, next_attempt_at) | 180d | delete | 500 | none |
| `assurance.cursors` | assurance.assurance_cursors | internal | — | — | retain_state | — | none |
| `assurance.findings.active` | assurance.assurance_findings | financial | — | — | retain_permanent | — | none |
| `assurance.findings.resolved` | assurance.assurance_findings | financial | resolved_at | 365d | delete | 500 | resource |
| `assurance.incident_summaries` | assurance.assurance_incident_summaries | internal | — | — | retain_permanent | — | none |
| `assurance.intake_commands` | assurance.intake_control_commands | personal | applied_at | 365d | delete | 500 | none |
| `assurance.retention_audit` | assurance.assurance_retention_audit | internal | — | — | retain_permanent | — | none |
| `assurance.retention_holds` | assurance.assurance_retention_holds | internal | — | — | retain_state | — | none |
| `assurance.runs.failed` | assurance.assurance_runs | internal | finished_at | 180d | delete | 500 | none |
| `assurance.runs.succeeded` | assurance.assurance_runs | internal | finished_at | 90d | delete | 500 | none |

**`assurance.alert_deliveries`** — docs/roadmap/archive/51 §4.2 explicitly covers "failed alert delivery" at 180d; extended here to delivered rows too for one consistent rule across the table (T0 judgment call — not separately itemized in §4.2).

**`assurance.cursors`** — One-row-per-source pipeline cursor singleton; not an event history table.

**`assurance.findings.active`** — docs/roadmap/archive/51 §4.1: retained while status IN ('open','acknowledged'). Never age-purged regardless of policy version.

**`assurance.findings.resolved`** — docs/roadmap/archive/51 §4.2 'Assurance resolved finding'. Only status='resolved' rows are ever eligible.

**`assurance.incident_summaries`** — Durable, hashed successor proof for a deleted failed run; intentionally survives assurance.runs.failed retention.

**`assurance.intake_commands`** — docs/roadmap/archive/51 §4.2 'Applied/rejected intake command'; pending/applying rows are never eligible. Same rule applied to payin/payout's equivalent intake_commands tables below for consistency across the three services that share this control-plane pattern.

**`assurance.retention_audit`** — docs/roadmap/archive/51 K4. Same shape/rationale as adminbff.retention_audit.

**`assurance.retention_holds`** — docs/roadmap/archive/51 K5. Same shape/rationale as adminbff.retention_holds.

**`assurance.runs.failed`** — docs/roadmap/archive/51 §4.2 'Assurance failed run'. Requires status='failed' and an incident/audit summary to already exist (§4.3).

**`assurance.runs.succeeded`** — docs/roadmap/archive/51 §4.2 'Assurance successful run'. Eligibility requires status='succeeded'.

## auth

| Class | Table / object | Classification | Terminal timestamp | Duration | Action | Batch | Hold scope |
|---|---|---|---|---|---|---|---|
| `auth.credentials` | auth.auth_credentials | secret | — | — | pseudonymize_on_closure | — | subject |
| `auth.kyc_apply_retries.dead` | auth.kyc_apply_retries | personal | updated_at | 365d | delete | 500 | subject |
| `auth.kyc_apply_retries.succeeded` | auth.kyc_apply_retries | personal | updated_at | 90d | delete | 500 | subject |
| `auth.kyc_document_object` | kyc document bytes (object store) | sensitive | paired auth.kyc_documents row's own terminal_timestamp | 365d | delete | 100 | subject |
| `auth.kyc_documents` | auth.kyc_documents | sensitive | auth_users closure time (join on kyc_documents.user_id = auth_users.id) | 365d | delete | 500 | subject |
| `auth.kyc_level_changes` | auth.kyc_level_changes | personal | — | — | pseudonymize_on_closure | — | subject |
| `auth.kyc_retry_summaries` | auth.auth_kyc_retry_summaries | internal | — | — | retain_permanent | — | none |
| `auth.kyc_submissions` | auth.kyc_submissions | sensitive | auth_users closure time (join on kyc_submissions.user_id = auth_users.id) | 365d | delete | 500 | subject |
| `auth.object_delete_outbox` | auth.auth_object_delete_outbox | internal | — | — | retain_permanent | — | none |
| `auth.operator_offboarding_requests` | auth.auth_operator_offboarding_requests | internal | — | — | retain_permanent | — | none |
| `auth.privacy_requests` | auth.privacy_requests | internal | — | — | retain_permanent | — | none |
| `auth.refresh_tokens` | auth.auth_refresh_tokens | secret | COALESCE(revoked_at, expires_at) | 30d | delete | 500 | subject |
| `auth.retention_audit` | auth.auth_retention_audit | internal | — | — | retain_permanent | — | none |
| `auth.retention_holds` | auth.auth_retention_holds | internal | — | — | retain_state | — | none |
| `auth.users` | auth.auth_users | personal | — | — | pseudonymize_on_closure | — | subject |

**`auth.credentials`** — 1:1 with auth_users; removed only as part of T5 closure finalization, never by an independent age rule.

**`auth.kyc_apply_retries.dead`** — docs/roadmap/archive/51 §4.2 'Dead KYC apply retry'. Requires status='dead' and an audit summary to already exist (§4.3).

**`auth.kyc_apply_retries.succeeded`** — docs/roadmap/archive/51 §4.2 'Successful KYC apply retry'. Requires status='succeeded'.

**`auth.kyc_document_object`** — Object key is "kyc/<document_uuid>" (internal/auth/documents.go, docs/roadmap/archive/51 T2.3) — opaque, carries no user reference; the document/user relationship lives only in the encrypted auth.kyc_documents row. Deletion must use K6's delete-intent-then-delete-then-mark outbox pattern, paired 1:1 with that metadata row. The Compose auth service wires the local file-backed object store; host binaries require OBJECT_STORE_DIR, and the feature fails closed when it is absent.

**`auth.kyc_documents`** — Same closure-gated rule as auth.kyc_submissions (grouped together in §4.2). auth-service wires FileDocumentStore when OBJECT_STORE_DIR is configured; Upload/Download fail closed without the store or document key ring. The paired object is deleted through auth_object_delete_outbox before metadata removal.

**`auth.kyc_level_changes`** — docs/roadmap/archive/51 §4.2: retained as 'pseudonymous level-change audit' even after the owning KYC submission is deleted — never age-deleted, only pseudonymized on closure (K11).

**`auth.kyc_retry_summaries`** — Durable hashed successor proof retained after a dead KYC retry is removed.

**`auth.kyc_submissions`** — docs/roadmap/archive/51 §4.2 'KYC submission and document': eligible only when the owning account has been closed more than 365 days and no hold applies — this is a closure-gated rule, not a standalone age rule on created_at/decided_at. `payload` becomes K2 ciphertext before this ever runs. The current schema uses the completed closure request's ready_at as the retention horizon rather than a separate closed_at column.

**`auth.object_delete_outbox`** — docs/roadmap/archive/51 K6 (T1.6). pkg/objectoutbox's transactional outbox for object-store deletes — 'done' rows are the permanent proof an object-delete intent was actually carried out, same append-only rationale as auth.retention_audit. Never age-purged.

**`auth.operator_offboarding_requests`** — docs/roadmap/archive/51 K10 (A8 T5b). Maker-checker record for operator/admin account offboarding — same shape and rationale as ledger.pending_adjustments: requested_by/approved_by are operator identities (never the offboarded subject's own PII), reason is an operator-authored justification, and the row is the permanent two-person-control audit trail proving who proposed and who approved each operator closure. Retained permanently, same append-only rationale as auth.retention_audit.

**`auth.privacy_requests`** — docs/roadmap/archive/51 K9 (T4). The ROW is the minimal audit tombstone K9's own wording requires: id/user_id/status/requested_at/ready_at/expires_at/downloaded_at/row_count — never the archive itself (that lives in the object store and is deleted via the auth object-delete outbox on download or 24h TTL expiry, both already covered by auth.object_delete_outbox above). No email/name/financial data ever lands in this table; object_key/manifest_hash are opaque storage/integrity pointers, not personal data. Retained permanently as the operator-visible record of 'this user requested an export on this date,' same append-only rationale as auth.retention_audit.

**`auth.refresh_tokens`** — docs/roadmap/archive/51 §4.2 'Expired/revoked refresh token'. Live (non-revoked, non-expired) tokens are never eligible.

**`auth.retention_audit`** — docs/roadmap/archive/51 K4. Same shape/rationale as adminbff.retention_audit.

**`auth.retention_holds`** — docs/roadmap/archive/51 K5. Auth coordinates hold commands across owners but still persists its own local copy here, same as every other owner.

**`auth.users`** — Never age-purged or generically deleted — migration comment: "users are disabled (status), never erased." email/full_name become K2 ciphertext; identity is only ever replaced with the T5 closure saga's surrogate.

## fraud

| Class | Table / object | Classification | Terminal timestamp | Duration | Action | Batch | Hold scope |
|---|---|---|---|---|---|---|---|
| `fraud.redis_velocity` | redis:fraud:velocity:<userID>:<YYYY-MM-DD-HH> and fraud:velocity:event:<eventID> dedup markers | financial | — | — | retain_state | — | subject |
| `fraud.retention_audit` | fraud.fraud_retention_audit | internal | — | — | retain_permanent | — | none |
| `fraud.retention_holds` | fraud.fraud_retention_holds | internal | — | — | retain_state | — | none |
| `fraud.sanctions_entries` | fraud.sanctions_entries | personal | — | — | self_replacing | — | none |
| `fraud.screening_event_summaries` | fraud.fraud_screening_event_summaries | financial | — | — | retain_permanent | — | none |
| `fraud.screening_events` | fraud.screening_events | financial | created_at | 365d | delete | 500 | subject |
| `fraud.screening_rule_modes` | fraud.screening_rule_modes | internal | — | — | retain_state | — | none |

**`fraud.redis_velocity`** — internal/fraud/rules.VelocityKey; self-expiring via Redis EXPIRE (~2h window), not a Postgres retention job. Reconstructable from posted ledger transactions plus published outbox proof at any time (A7 K10) — never a data-loss event on expiry.

**`fraud.retention_audit`** — docs/roadmap/archive/51 K4. Same shape/rationale as adminbff.retention_audit.

**`fraud.retention_holds`** — docs/roadmap/archive/51 K5. Same shape/rationale as adminbff.retention_holds.

**`fraud.sanctions_entries`** — Third-party (non-platform-user) sanctions/watchlist data from an external dataset. internal/fraud/repository/sanctions_repository.go already fully replaces the table on every dataset load (DELETE-all + re-INSERT) — no independent age-based job needed or wanted; a stale entry must not silently persist past its dataset version.

**`fraud.screening_event_summaries`** — Non-identifying daily/rule/verdict aggregate successor proof, persisted atomically before screening event deletion.

**`fraud.screening_events`** — docs/roadmap/archive/51 §4.2 'Fraud screening event'. Requires aggregate audit metrics to already be recorded (§4.3) before deletion.

**`fraud.screening_rule_modes`** — Live per-rule config row (off/monitor/block); not an event history table.

## gateway

| Class | Table / object | Classification | Terminal timestamp | Duration | Action | Batch | Hold scope |
|---|---|---|---|---|---|---|---|
| `gateway.merchant.api_key_scopes` | gateway.merchant_api_key_scopes | internal | — | — | retain_state | — | none |
| `gateway.merchant.api_keys_revoked` | gateway.merchant_api_keys | secret | revoked_at | 90d | delete | 500 | subject |
| `gateway.merchant.event_inbox` | gateway.merchant_event_inbox | internal | processed_at | 30d | delete | 500 | none |
| `gateway.merchant.idempotency_records` | gateway.merchant_idempotency_records | sensitive | expires_at | 24h | delete | 500 | subject |
| `gateway.merchant.quota_policies` | gateway.merchant_quota_policies | internal | — | — | retain_state | — | none |
| `gateway.merchant.settings` | gateway.merchant_settings | internal | — | — | retain_state | — | none |
| `gateway.merchant.tenant_lifecycle_requests` | gateway.merchant_tenant_lifecycle_requests | internal | — | — | retain_permanent | — | none |
| `gateway.merchant.tenants` | gateway.merchant_tenants | internal | — | — | retain_state | — | none |
| `gateway.merchant.webhook_attempts` | gateway.merchant_webhook_attempts | internal | — | — | retain_state | — | none |
| `gateway.merchant.webhook_deliveries` | gateway.merchant_webhook_deliveries | internal | updated_at | 90d | delete | 500 | subject |
| `gateway.merchant.webhook_endpoints` | gateway.merchant_webhook_endpoints | secret | — | — | retain_state | — | none |
| `gateway.merchant.webhook_events` | gateway.merchant_webhook_events | sensitive | created_at | 90d | delete | 500 | subject |
| `gateway.notifications.any` | gateway.notif_notifications | financial | created_at | 365d | delete | 500 | subject |
| `gateway.notifications.channel_controls` | gateway.notif_channel_controls | internal | — | — | retain_state | — | none |
| `gateway.notifications.delivery_attempts` | gateway.notif_delivery_attempts | internal | finished_at | 90d | delete | 500 | resource |
| `gateway.notifications.deliveries` | gateway.notif_deliveries | internal | updated_at | 180d | delete | 500 | subject |
| `gateway.notifications.device_tokens` | gateway.notif_device_endpoints | secret | revoked_at | 30d | redact | 500 | subject |
| `gateway.notifications.digest_items` | gateway.notif_digest_items | financial | updated_at | 90d | delete | 500 | subject |
| `gateway.notifications.digest_windows` | gateway.notif_digest_windows | internal | updated_at | 90d | delete | 500 | subject |
| `gateway.notifications.event_inbox` | gateway.notif_event_inbox | internal | processed_at | 30d | delete | 500 | resource |
| `gateway.notifications.event_inbox_failed` | gateway.notif_event_inbox | internal | updated_at | 90d | delete | 500 | resource |
| `gateway.notifications.preferences` | gateway.notif_preferences | personal | — | — | retain_state | — | subject |
| `gateway.notifications.recipient_ciphertext` | gateway.notif_deliveries | sensitive | updated_at | 30d | redact | 500 | subject |
| `gateway.notifications.settings` | gateway.notif_user_settings | personal | — | — | retain_state | — | subject |
| `gateway.notifications.template_versions` | gateway.notif_template_versions | internal | — | — | retain_permanent | — | none |
| `gateway.notifications.templates` | gateway.notif_templates | internal | — | — | retain_state | — | none |
| `gateway.notifications.read` | gateway.notif_notifications | financial | read_at | 180d | delete | 500 | subject |
| `gateway.retention_audit` | gateway.gateway_retention_audit | internal | — | — | retain_permanent | — | none |
| `gateway.retention_holds` | gateway.gateway_retention_holds | internal | — | — | retain_state | — | none |

**`gateway.merchant.api_key_scopes`** — No independent timestamp column; rows are deleted transactionally alongside their parent merchant_api_keys row (gateway.merchant.api_keys_revoked), not by an independent age rule.

**`gateway.merchant.api_keys_revoked`** — Only revoked keys age out; active/expired-but-not-revoked keys are untouched. Cascades its own merchant_api_key_scopes rows in the same purge transaction.

**`gateway.merchant.event_inbox`** — Requires processed_at IS NOT NULL — an unprocessed inbox row is never purged, regardless of age.

**`gateway.merchant.idempotency_records`** — Same expiry-column-driven pattern as ledger.fee_quotes.unconsumed (T2.6 precedent) — purges shortly after the record's own expires_at, not a fixed age from creation. May contain tenant response bodies (sensitive), hence classification.

**`gateway.merchant.quota_policies`** — Live per-tenant quota configuration; no generic age rule applies.

**`gateway.merchant.settings`** — Plan 57 T9. Generic operational key/value store — currently one row, b2b_api_enabled (the global route-disable kill switch). Live configuration, not an event log; no generic age rule applies, same posture as gateway.merchant.quota_policies.

**`gateway.merchant.tenant_lifecycle_requests`** — Plan 57 T8. Maker-checker record for tenant live-mode activation and closure — same shape and rationale as auth.operator_offboarding_requests: requested_by/approved_by are OPERATOR identities (never the tenant's own data), reason is an operator-authored justification, and the row is the permanent two-person-control audit trail proving who proposed and who approved each sensitive tenant transition. Retained permanently, same append-only rationale as auth.retention_audit.

**`gateway.merchant.tenants`** — Live tenant configuration row; no generic age rule. Tenant closure is a future operator workflow, not an automatic purge.

**`gateway.merchant.webhook_attempts`** — No independent age rule — rows are removed transactionally via ON DELETE CASCADE when their parent merchant_webhook_deliveries row is purged (gateway.merchant.webhook_deliveries).

**`gateway.merchant.webhook_deliveries`** — Requires status IN ('delivered','dead') — a pending/failed (still retrying) delivery is never purged regardless of age. merchant_webhook_attempts cascade-delete with their parent row (ON DELETE CASCADE), so they need no independent purge function.

**`gateway.merchant.webhook_endpoints`** — Live endpoint configuration (secret_ciphertext is already encrypted via pkg/cryptox, T7); deletion is an explicit merchant/operator action (DELETE /webhook-endpoints/{id}), not an automatic age rule.

**`gateway.merchant.webhook_events`** — Immutable external event bytes, purged once no merchant_webhook_deliveries row still references them (enforced in the purge function itself, not just the age check) — payload may carry tenant transaction data.

**`gateway.notifications.any`** — docs/roadmap/archive/51 §4.2 'Any notification' — backstop rule for unread notifications regardless of read_at.

**`gateway.notifications.channel_controls`** — The current global channel control is live safety state; each change is also recorded by the Admin BFF audit path.

**`gateway.notifications.delivery_attempts`** — Finished provider-attempt evidence is bounded and sanitized; in-flight attempts remain until they finish.

**`gateway.notifications.deliveries`** — Terminal delivery rows and their attempt evidence are removed after the operational evidence window; pending and processing work is never purged.

**`gateway.notifications.device_tokens`** — Invalid or revoked push-token ciphertext is erased after the grace period; endpoint identity and lifecycle status remain.

**`gateway.notifications.digest_items`** — Digest membership is planning metadata and is removed with terminal windows after 90 days.

**`gateway.notifications.digest_windows`** — Terminal local-time digest windows are bounded operational state; delivery evidence has its own longer retention class.

**`gateway.notifications.event_inbox`** — Successful event-inbox rows retain only the deduplication evidence needed for the recovery window; unprocessed rows are never age-purged.

**`gateway.notifications.event_inbox_failed`** — Failed notification planning evidence is retained longer for incident diagnosis; the stored payload is only a hash and approved error code.

**`gateway.notifications.preferences`** — Current category/channel preferences are live user configuration and have no independent age purge.

**`gateway.notifications.recipient_ciphertext`** — Terminal email recipient ciphertext and its fingerprint are erased after the short operational window; delivery status remains.

**`gateway.notifications.settings`** — Current notification settings are live user configuration; account closure pseudonymizes or removes them through the A8 owner workflow.

**`gateway.notifications.template_versions`** — Immutable template versions, hashes, and maker-checker transitions are long-lived operational evidence.

**`gateway.notifications.templates`** — Template catalog rows define the governed notification-kind contract and remain as current operational state.

**`gateway.notifications.read`** — docs/roadmap/archive/51 §4.2 'Read notification'. Requires read_at IS NOT NULL.

**`gateway.retention_audit`** — docs/roadmap/archive/51 K4. Same shape/rationale as adminbff.retention_audit.

**`gateway.retention_holds`** — docs/roadmap/archive/51 K5. Same shape/rationale as adminbff.retention_holds.

## ledger

| Class | Table / object | Classification | Terminal timestamp | Duration | Action | Batch | Hold scope |
|---|---|---|---|---|---|---|---|
| `ledger.account_balance_snapshots` | ledger.account_balance_snapshots | financial | — | — | retain_permanent | — | none |
| `ledger.account_balances` | ledger.account_balances | financial | — | — | retain_permanent | — | none |
| `ledger.accounts` | ledger.accounts | financial | — | — | retain_permanent | — | none |
| `ledger.chargeback_dispute_status_changes` | ledger.chargeback_dispute_status_changes | financial | — | — | retain_permanent | — | none |
| `ledger.chargeback_disputes` | ledger.chargeback_disputes | financial | — | — | retain_permanent | — | none |
| `ledger.currencies` | ledger.currencies | public | — | — | retain_state | — | none |
| `ledger.disbursement_batches` | ledger.disbursement_batches | internal | — | — | retain_permanent | — | none |
| `ledger.disbursement_items` | ledger.disbursement_items | financial | — | — | retain_permanent | — | none |
| `ledger.fee_quotes.consumed` | ledger.fee_quotes | financial | consumed_at | 365d | delete | 500 | none |
| `ledger.fee_quotes.unconsumed` | ledger.fee_quotes | financial | expires_at | 24h | delete | 500 | none |
| `ledger.fee_rules` | ledger.fee_rules | financial | — | — | retain_state | — | none |
| `ledger.ledger_entries` | ledger.ledger_entries | financial | — | — | retain_immutable | — | none |
| `ledger.ledger_transactions` | ledger.ledger_transactions | financial | — | — | retain_permanent | — | none |
| `ledger.outbox_events.dead` | ledger.outbox_events | financial | — | — | never_automatic | — | none |
| `ledger.outbox_events.published` | ledger.outbox_events | financial | published_at | 30d | delete | 500 | none |
| `ledger.pending_adjustments` | ledger.pending_adjustments | financial | — | — | retain_permanent | — | none |
| `ledger.policy_limits` | ledger.policy_limits | financial | — | — | retain_state | — | none |
| `ledger.policy_tier_limits` | ledger.policy_tier_limits | internal | — | — | retain_state | — | none |
| `ledger.recon_batches` | ledger.recon_batches | internal | created_at (status IN ('completed','failed')) | 90d | redact | 500 | resource |
| `ledger.recon_items` | ledger.recon_items | financial | parent recon_batches row's own terminal_timestamp (join on recon_items.batch_id = recon_batches.id) | 90d | redact | 500 | resource |
| `ledger.redis_policy_counters` | redis:pol:<userID>:<txType>:{d\|m}:<period>:{amt\|cnt} | financial | — | — | retain_state | — | subject |
| `ledger.retention_audit` | ledger.ledger_retention_audit | internal | — | — | retain_permanent | — | none |
| `ledger.retention_holds` | ledger.ledger_retention_holds | internal | — | — | retain_state | — | none |
| `ledger.savings_config` | ledger.savings_config | financial | — | — | retain_state | — | none |
| `ledger.scheduled_transactions` | ledger.scheduled_transactions | financial | updated_at | 365d | delete | 500 | subject |
| `ledger.transactions.idempotency_raw` | ledger.ledger_transactions | secret | updated_at | 30d | redact | 500 | none |

**`ledger.account_balance_snapshots`** — Dated historical snapshot; the daily job may DELETE+re-INSERT a single date's rows to correct a bad snapshot, which is a correctness fix, not an age-based retention action.

**`ledger.account_balances`** — Current-state balance projection; not an event history row.

**`ledger.accounts`** — owner_id is pseudonymized (not purged) by the T5 closure saga per K10/K11; otherwise never age-purged.

**`ledger.chargeback_dispute_status_changes`** — Security audit finding (migrations/ledger/000037_chargeback_dispute_audit_trail) — the actor/history trail for chargeback_disputes resolution; same permanent-retention posture as its parent table (an accountability record has no reason to expire before the case it documents does).

**`ledger.chargeback_disputes`** — Business-completeness audit finding (migrations/ledger/000035_chargeback_disputes) — card-network dispute case evidence, same permanent-retention posture as disbursement_items/recon_batches: a resolved dispute (won/lost/expired) is exactly the kind of record a card network or regulator can re-open years later.

**`ledger.currencies`** — Static reference table.

**`ledger.disbursement_batches`** — Not itemized in §4.2 — T0 judgment call, treated as financial-adjacent batch evidence akin to recon_batches/disbursement_items, retained like other permanent financial batch records.

**`ledger.disbursement_items`** — Not itemized in §4.2 — T0 judgment call. Each row ties to posted_tx_id once posted; treated as permanent financial evidence like disbursement_batches.

**`ledger.fee_quotes.consumed`** — docs/roadmap/archive/51 §4.2/K8. Requires consumed_by_ref to still point at a terminal transaction/payout with matching booked-fee proof — K8's proof-aware gate, not a bare age check.

**`ledger.fee_quotes.unconsumed`** — docs/roadmap/archive/51 §4.2/K8. Requires consumed_at IS NULL. A quote a concurrent consumer is locking is skipped this run, never deleted underneath consumption (K8).

**`ledger.fee_rules`** — Live pricing configuration, not event history. A disabled/superseded rule needs its own explicit future policy per docs/roadmap/archive/51 §4.2's closing note, not a generic age rule.

**`ledger.ledger_entries`** — docs/roadmap/archive/51 §4.1. DB trigger fn_prevent_entry_mutation rejects UPDATE/DELETE regardless of caller — structurally, not just policy, immutable.

**`ledger.ledger_transactions`** — docs/roadmap/archive/51 §4.1 'posted transaction headers, lifecycle closers'. idempotency_key/idempotency_scope become K7 digest tombstones in T3, handled by a separate class below — this class covers everything else in the row.

**`ledger.outbox_events.dead`** — docs/roadmap/archive/51 §4.2 'Dead ledger outbox event' — never automatic at any age; an operator must resolve or replay it first.

**`ledger.outbox_events.published`** — docs/roadmap/archive/51 §4.2 'Published ledger outbox event'. Requires status='published'.

**`ledger.pending_adjustments`** — docs/roadmap/archive/51 §4.1 'pending adjustments and executed reconciliation decisions'. cmd_payload is a governance/maker-checker audit record, retained indefinitely.

**`ledger.policy_limits`** — Live per-user/default limit configuration, not event history.

**`ledger.policy_tier_limits`** — Static per-KYC-tier template, not per-user or event history.

**`ledger.recon_batches`** — docs/roadmap/archive/51 §4.2 'Reconciliation raw row/source filename': redacts source_filename only, once the batch is terminal for 90+ days. row_count/status/gateway/report_date remain. T0 finding: recon_batches has no updated_at/completed_at column to mark the terminal-status transition (per T0's inventory) — created_at is used as a close-enough proxy since reconciliation batches process quickly after creation; T1 should add a real completion timestamp if that assumption ever stops holding.

**`ledger.recon_items`** — docs/roadmap/archive/51 §4.2. Redacts `raw` only, once the parent recon_batches row is terminal for 90+ days. amount/match_status/matched_tx_id remain, matching 'retain match result and totals'.

**`ledger.redis_policy_counters`** — internal/policy.DailyAmountKey/DailyCountKey/MonthlyAmountKey. Self- expiring counters (Redis key TTL, not a Postgres retention job) — daily keys naturally roll off within ~48h, monthly within ~35d, matching docs/roadmap/archive/50 T5's own drreseed reconstruction assumptions. Reconstructable from posted ledger transactions at any time (A7 K10), so a lost/expired key is never a data-loss event.

**`ledger.retention_audit`** — docs/roadmap/archive/51 K4. Same shape/rationale as adminbff.retention_audit.

**`ledger.retention_holds`** — docs/roadmap/archive/51 K5. Same shape/rationale as adminbff.retention_holds.

**`ledger.savings_config`** — Live per-account interest configuration, not event history.

**`ledger.scheduled_transactions`** — Not itemized in docs/roadmap/archive/51 §4.2 — a T0 judgment call, by analogy to the 365-day "terminal operational row" pattern used for payout_vendor_commands and intake_control_commands. Requires status IN ('finished','failed'). cmd_payload carries the scheduled transfer's own destination/amount, so this class is financial, not merely internal.

**`ledger.transactions.idempotency_raw`** — docs/roadmap/archive/51 K7: nulls idempotency_key/idempotency_scope 30 days after the transaction reaches a terminal status, once T3's digest/version/ conflict-fingerprint columns exist and are backfilled. Requires status IN ('posted','failed','reversed'). Never touches the row's financial fields.

## payin

| Class | Table / object | Classification | Terminal timestamp | Duration | Action | Batch | Hold scope |
|---|---|---|---|---|---|---|---|
| `payin.intake_commands` | payin.payin_intake_commands | personal | created_at | 365d | delete | 500 | none |
| `payin.intake_control` | payin.payin_intake_control | internal | — | — | retain_state | — | none |
| `payin.retention_audit` | payin.payin_retention_audit | internal | — | — | retain_permanent | — | none |
| `payin.retention_holds` | payin.payin_retention_holds | internal | — | — | retain_state | — | none |
| `payin.routing_rules` | payin.payin_routing_rules | financial | — | — | retain_state | — | none |
| `payin.topup_intents` | payin.payin_topup_intents | financial | — | — | retain_permanent | — | none |
| `payin.vendor_gateways` | payin.payin_vendor_gateways | internal | — | — | retain_state | — | none |
| `payin.webhook_events.raw` | payin.payin_webhook_events | sensitive | updated_at | 30d | redact | 500 | subject |

**`payin.intake_commands`** — docs/roadmap/archive/51 §4.2 'Applied/rejected intake command'. Requires applied=true; a pending command is never eligible.

**`payin.intake_control`** — Singleton control row (id=1).

**`payin.retention_audit`** — docs/roadmap/archive/51 K4. Same shape/rationale as adminbff.retention_audit.

**`payin.retention_holds`** — docs/roadmap/archive/51 K5. Same shape/rationale as adminbff.retention_holds.

**`payin.routing_rules`** — Live routing configuration, not event history.

**`payin.topup_intents`** — docs/roadmap/archive/51 §4.1 'posted event and settled-intent correlation fields'. An expired-and-unresolved intent is not itemized in §4.2 — left as retain_permanent (T0 judgment call) since it is small, low-sensitivity, and directly correlates to a real or attempted top-up.

**`payin.vendor_gateways`** — Static vendor→gateway routing map.

**`payin.webhook_events.raw`** — docs/roadmap/archive/51 §4.2 'Pay-in raw webhook body'. Requires status IN ('posted','failed','blocked'). Redacts `raw` only; vendor/vendor_event_id/external_ref/amount/currency/status remain (the 'allowlisted correlation columns').

## payout

| Class | Table / object | Classification | Terminal timestamp | Duration | Action | Batch | Hold scope |
|---|---|---|---|---|---|---|---|
| `payout.intake_commands` | payout.payout_intake_commands | personal | created_at | 365d | delete | 500 | none |
| `payout.intake_control` | payout.payout_intake_control | internal | — | — | retain_state | — | none |
| `payout.requests` | payout.payout_requests | financial | — | — | retain_permanent | — | none |
| `payout.requests.destination_and_error` | payout.payout_requests | sensitive | updated_at | 30d | redact | 500 | subject |
| `payout.retention_audit` | payout.payout_retention_audit | internal | — | — | retain_permanent | — | none |
| `payout.retention_holds` | payout.payout_retention_holds | internal | — | — | retain_state | — | none |
| `payout.routing_rules` | payout.payout_routing_rules | financial | — | — | retain_state | — | none |
| `payout.vendor_calls` | payout.payout_vendor_calls | internal | — | — | retain_permanent | — | none |
| `payout.vendor_commands` | payout.payout_vendor_commands | internal | updated_at | 365d | delete | 500 | none |
| `payout.vendor_gateways` | payout.payout_vendor_gateways | internal | — | — | retain_state | — | none |

**`payout.intake_commands`** — docs/roadmap/archive/51 §4.2 'Applied/rejected intake command'. Requires applied=true.

**`payout.intake_control`** — Singleton control row (id=1).

**`payout.requests`** — docs/roadmap/archive/51 §4.1 'amount, currency, vendor, state, hold/closer IDs, fee proof'. `destination`/`error_message` are handled by the separate redact class below.

**`payout.requests.destination_and_error`** — docs/roadmap/archive/51 §4.1/§4.2 'Payout destination and raw error'. Requires status IN ('settled','failed','cancelled','rejected'). Redacts destination+error_message only.

**`payout.retention_audit`** — docs/roadmap/archive/51 K4. Same shape/rationale as adminbff.retention_audit.

**`payout.retention_holds`** — docs/roadmap/archive/51 K5. Same shape/rationale as adminbff.retention_holds.

**`payout.routing_rules`** — Live routing configuration, not event history.

**`payout.vendor_calls`** — Append-only vendor-call audit trail by design (migration comment: never DELETE). req_summary is already a sanitized summary, never a full payload — no redaction needed.

**`payout.vendor_commands`** — docs/roadmap/archive/51 §4.2 'Payout vendor call/command'. Requires status IN ('completed','dead'); dead commands additionally require the operator to have already reviewed them (§4.3 'successor/audit summary').

**`payout.vendor_gateways`** — Static vendor→gateway routing map.

## shared

| Class | Table / object | Classification | Terminal timestamp | Duration | Action | Batch | Hold scope |
|---|---|---|---|---|---|---|---|
| `shared.a7_backups` | pgBackRest encrypted backup chains (docs/roadmap/archive/50) | financial | — | — | expiration_based | — | none |
| `shared.logs_and_traces` | application logs (Loki) and traces (Tempo) | personal | — | — | protected_by_masking | — | none |
| `shared.rabbitmq_event_transit` | RabbitMQ in-flight event delivery (internal/ledger/events) | financial | — | — | not_persisted | — | none |
| `shared.redis_circuit_breaker` | redis:breaker:<namespace>:{state\|probe}:<vendor>, breaker:<namespace>:vendors | internal | — | — | retain_state | — | none |
| `shared.redis_job_locks` | redis:joblock:<jobName> | internal | — | — | retain_state | — | none |
| `shared.redis_rate_limits` | redis:rl:ip:<ip>, rl:user:<userID>, rl:<ip>:<path>, rl:webhook:<vendor> | internal | — | — | retain_state | — | none |

**`shared.a7_backups`** — docs/roadmap/archive/51 K12: active-database redaction/deletion does not rewrite already-taken backups. A redacted/deleted value may still be readable from an encrypted backup chain until that chain expires per A7 K4 (two retained full chains + their WAL). Privacy status responses and runbooks must state the latest backup-expiration horizon rather than claim complete erasure. Not independently age-purged by this policy — governed entirely by docs/roadmap/archive/50-a7-backup-pitr-disaster-recovery.md K4.

**`shared.logs_and_traces`** — docs/roadmap/archive/51 §2.4: passwords, tokens, authorization values, documents, raw webhook fields, payout destinations, and full idempotency keys are masked at the point of logging (pkg/logger), not retained-then-purged. Loki/Tempo's own configured retention window governs storage duration for whatever remains (deploy/observability/); this class documents the protection mechanism rather than defining a new database-style purge job, satisfying docs/roadmap/archive/51 §6 T0's "logs, traces... are classified" requirement.

**`shared.rabbitmq_event_transit`** — Events (TransactionPosted, TransactionReversed, AdjustmentDecided) exist only in flight; the durable fact lives in ledger.outbox_events (its own class above) and the consumer's own persisted row (gateway.notif_notifications, its own class above). A broker-only in-flight delivery lost to a broker outage is recovered by outbox replay (A7 K10), never treated as data requiring its own retention rule.

**`shared.redis_circuit_breaker`** — internal/vendorgw/distributed_breaker.go (used by payin+payout). Operational state only, no personal data; safe to start empty after any restore.

**`shared.redis_job_locks`** — pkg/scheduler.NewRedisLock. Short-lived distributed cron locks; no personal data.

**`shared.redis_rate_limits`** — pkg/middleware/rate_limit.go. Self-expiring fixed-window counters; safe to start empty after any restore (A7 K10 §4).

## vendor

| Class | Table / object | Classification | Terminal timestamp | Duration | Action | Batch | Hold scope |
|---|---|---|---|---|---|---|---|
| `vendor.callback_inbox` | vendor.vendor_callback_inbox | sensitive | updated_at | 30d | redact | 500 | none |
| `vendor.outbound_attempts` | vendor.vendor_outbound_attempts | internal | — | — | retain_permanent | — | none |
| `vendor.retention_audit` | vendor.vendor_retention_audit | internal | — | — | retain_permanent | — | none |
| `vendor.retention_holds` | vendor.vendor_retention_holds | internal | — | — | retain_state | — | none |

**`vendor.callback_inbox`** — Raw callback bytes are redacted after the investigation window; sanitized correlation fields remain for reconciliation.

**`vendor.outbound_attempts`** — Append-only sanitized vendor transport audit; plaintext credentials and full vendor payloads are never stored.

**`vendor.retention_audit`** — docs/roadmap/archive/51 K4. Same shape/rationale as adminbff.retention_audit.

**`vendor.retention_holds`** — docs/roadmap/archive/51 K5. Same shape/rationale as adminbff.retention_holds.
