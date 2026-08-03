# Notification Auth contact outage

Impact: email remains pending-recipient; in-app creation and financial processing continue independently.

Diagnosis: inspect bounded Auth resolver errors, timeout rate, mTLS identity, internal-token configuration, and contact-resolution lag.

Safe action: do not requeue the source domain event. Keep the delivery pending/retry_wait and restore the purpose-built Auth contract.

Recovery: verify active/verified contact responses, then allow workers to drain with the configured backoff.

Replay warning: contact resolution retry is safe; provider replay after acceptance is not exactly-once.

Verify: no email appears in logs/metrics, verified-only delivery resumes, and in-app counts are unchanged.
