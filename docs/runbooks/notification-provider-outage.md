# Notification provider outage

Impact: the affected external channel accumulates retry/dead work; in-app remains the source of truth and money movement is unaffected.

Diagnosis: classify adapter results as transient, permanent, invalid endpoint, or accepted. Inspect control state and oldest due age.

Safe action: pause the channel and set an expiry/reason. Preserve rendered snapshots and attempt evidence.

Recovery: verify the local adapter/provider, resume or drain-only, and replay only after a checker-approved decision.

Replay warning: an acceptance crash window can produce duplicate external delivery even with idempotency keys.

Verify: bounded backlog drain, provider result rate, dead delivery growth, and absence of credentials/recipient data in evidence.
