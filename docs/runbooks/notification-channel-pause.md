# Notification channel pause

Impact: pause stops new external claims for the selected channel; in-app planning and financial processing continue.

Diagnosis: inspect channel state, reason, expiry, backlog age, and the Admin BFF audit entry.

Safe action: use `paused` for an incident and `drain_only` when existing work may safely drain. In-app has no ordinary global pause.

Recovery: resume only after the provider/template/contact condition is understood; keep a bounded backlog guard.

Replay warning: resuming existing work is not a replay, but explicit replay can duplicate provider-accepted messages.

Verify: state/expiry, claim rate, oldest due age, and audit/CSRF evidence.
