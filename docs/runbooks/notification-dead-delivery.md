# Notification dead delivery

Impact: one external channel attempt is exhausted or permanently rejected; the in-app notification is retained.

Diagnosis: inspect delivery status, stable error code, attempt history, template version, and endpoint/contact lifecycle without exposing secrets.

Safe action: fix the underlying policy/provider/contact issue before replay. A replay requires an operator reason and must not alter the snapshot.

Recovery: use the authorized delivery replay endpoint, then verify a new attempt sequence and terminal outcome.

Replay warning: replay can duplicate mail/push when the provider accepted an earlier attempt but the commit was lost.

Verify: attempt number, provider result class, delivery state transition, audit record, and in-app availability.
