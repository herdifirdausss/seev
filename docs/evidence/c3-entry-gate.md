# C3 Entry-Gate Evidence — Plan 59 T0

This document records the C3 inventory and the boundary decisions before
acceptance execution. The implementation is present in the current main
checkout; this record remains an acceptance checklist.

## Scope recorded

- Gateway remains the notification owner; no notification service was added.
- Existing list/read behavior remains additive and legacy rows remain readable.
- The initial source is `ledger.transaction.posted.v1` on
  `ledger.events.notifications`.
- Auth is reached only through the internal verified-contact contract.
- Local SMTP/Mailpit and mock push are the only initial external sinks.
- Privacy, retention, template, provider, and runbook references are linked from
  [the C3 reference index](../reference/notifications.md).

## Evidence status

| Gate | Status | Artifact |
|---|---|---|
| Current service/inbox inventory | recorded | [C3 current inventory](../reference/c3-current-notification-inventory.md) |
| Event source and mapping | recorded | [C3 event inventory](../reference/c3-event-source-inventory.md) |
| Privacy classes and closure/export behavior | recorded | [C3 privacy inventory](../reference/c3-privacy-inventory.md) |
| Threat model update | recorded | [security threat model](../security/threat-model.md) |
| Runtime/test/build/contract execution | completed | see [C3 final acceptance](c3-final-acceptance.md) |

Runtime and test execution were completed in the acceptance pass recorded in
the final-acceptance document. The implementation is now accepted at the
integration-test and admin-workflow level; chaos and load gates remain
outside the C3 baseline scope.
