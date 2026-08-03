# Notification duplicate external delivery

Impact: a user may receive duplicate email/push; in-app logical creation remains deduplicated and money movement is unaffected.

Diagnosis: correlate delivery ID, stable Message-ID/idempotency key, attempt history, provider acceptance, lease expiry, and worker restart. Do not infer a financial duplicate.

Safe action: pause the affected channel if the rate is rising; do not delete attempt evidence or rewrite rendered snapshots.

Recovery: fix lease/timeout/provider behavior, resume with bounded workers, and document the unavoidable acceptance/commit crash window.

Replay warning: every explicit replay carries the same duplicate risk and requires an operator reason.

Verify: one logical notification per event/user/kind, provider deduplication behavior, attempt sequence, and sanitized incident evidence.
