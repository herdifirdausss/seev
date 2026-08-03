# Notification template incident

Impact: incorrect copy affects only notification presentation; ledger, payout, payin, and account state are unchanged.

Diagnosis: compare immutable delivery snapshots, template version, locale, content hash, and the approved fixture. Do not edit an active version in place.

Safe action: retire the faulty version and pause the affected channel when exposure is ongoing. Preserve audit evidence.

Recovery: create a new draft, validate fixtures and headers/HTML, obtain checker approval, then replay only authorized blocked/dead rows.

Replay warning: provider acceptance cannot be recalled and replay may duplicate external delivery.

Verify: render snapshots, active-version lookup, channel backlog, and user-facing in-app history. Record version/hash/audit IDs only.
