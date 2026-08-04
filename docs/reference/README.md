# Current-System Reference

> [Documentation home](../README.md) · **Reference**

These documents describe the current system from different viewpoints. Open
one according to the question you are answering.

| Question | Document |
|---|---|
| What is implemented and verified in the current checkout? | [Current state](current-state.md) |
| Why is the system designed this way? | [Architecture](architecture.md) |
| What does each service own and expose? | [Services](services.md) |
| What belongs in shared `internal/platform/` code? | [Shared packages](shared-packages.md) |
| Why was a safety decision chosen? | [Rationale](rationale.md) |
| What does an unfamiliar term mean? | [Glossary](glossary.md) |
| What HTTP, protobuf, and event contracts are current? | [API contracts](api-contracts.md) |
| What does a ledger event mean on the wire? | [Event contract](events.md) |
| How are notifications planned and delivered? | [Notification platform](notifications.md) |
| Which notification kinds, templates, preferences, and providers exist? | [Notification references](notification-kinds.md) |
| Which code and test prove a claim? | [Traceability](traceability.md) |

Current references must agree with executable behavior. Future designs belong
in the [roadmap](../roadmap/README.md); incident procedures belong in
[operations](../operations/README.md).
