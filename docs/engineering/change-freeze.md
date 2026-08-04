# P0–P2 change-freeze policy

During the P0–P2 hardening window, non-critical feature work is frozen. A
change may enter the protected branch only if it is one of:

- a correctness, security, reliability, or operability fix;
- a migration required by an approved hardening item;
- a test, runbook, observability, or evidence improvement; or
- an explicitly approved rollback or incident response change.

The pull request must link one tracker row in
`docs/engineering/improvement-plan-tracker.md`, identify the owner, and state
the verification command. Product features remain queued until the go/no-go
scorecard is approved.

This policy is a repository control and review contract. GitHub branch rules,
labels, and milestones must be applied by a repository administrator using the
definitions in `.github/roadmap/metadata.yml`.
