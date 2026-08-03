# Notification email backlog

Impact: in-app notices continue; email is delayed or pending contact resolution. Financial requests never wait for email.

Diagnosis: inspect oldest due age, channel control, pending-recipient count, Auth contact latency, SMTP/Mailpit health, and dead/retry counts.

Safe action: pause email or switch to drain-only during provider instability. Do not delete pending deliveries or request contacts from the public API.

Recovery: restore Auth/SMTP, let bounded workers drain, and replay only dead/blocked rows with an operator reason.

Replay warning: stable Message-ID reduces duplicates but cannot provide exactly-once delivery.

Verify: accepted attempts, retry schedule, oldest due age, recipient ciphertext presence only as a boolean, and no plaintext in logs.
