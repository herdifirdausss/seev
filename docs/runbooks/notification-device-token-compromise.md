# Notification device-token compromise

Impact: the affected push endpoint may receive unauthorized previews; in-app and financial state are unaffected.

Diagnosis: identify endpoint IDs/platform/status and bounded invalid-device metrics. Never expose the token or decrypt it for an incident report.

Safe action: revoke the endpoint, pause push if scope is broad, erase ciphertext after the grace period or via approved security handling, and rotate provider credentials if applicable.

Recovery: register a fresh token through the authenticated device API and resume push only after security review.

Replay warning: do not replay deliveries to a compromised endpoint; other endpoints still have normal at-least-once semantics.

Verify: endpoint is revoked/invalid, token ciphertext is inaccessible/erased, no token plaintext appears in logs/exports, and audit evidence is complete.
