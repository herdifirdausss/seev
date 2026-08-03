# Notification push backlog

Impact: in-app notices continue; push is delayed. Money movement and registered devices are unaffected.

Diagnosis: inspect push control, oldest due age, provider result classes, invalid-device rate, and mock-provider accepted/deduplicated records.

Safe action: pause or drain-only push; preserve endpoint rows and attempt evidence. Never print or manually copy a token.

Recovery: restore the adapter, allow bounded retries, invalidate permanent endpoints, and replay only authorized dead/blocked deliveries.

Replay warning: provider acceptance followed by a worker crash can duplicate a push.

Verify: provider idempotency keys, invalid endpoint transitions, retry counts, and sanitized response excerpts.
