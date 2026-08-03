# Notification recipient data exposure

Impact: email address or device-token confidentiality may be at risk; financial state remains unchanged. Treat as a security incident.

Diagnosis: restrict access, inspect logs/audit/provider traces for plaintext, and identify the bounded delivery IDs without exporting ciphertext.

Safe action: pause the affected channel, revoke exposed devices, rotate encryption/fingerprint material according to the secret procedure, and preserve sanitized evidence.

Recovery: erase recipient ciphertext through retention/closure controls, restore a verified adapter, and require security approval before resuming.

Replay warning: replay is prohibited until exposure scope is closed; after approval it may duplicate external delivery.

Verify: no plaintext in logs/exports, endpoint state, ciphertext erasure result, audit trail, and notification health separated from product health.
