# Security Policy

> **License and safety are separate:** Seev is open source under
> [Apache-2.0](LICENSE), but it is still under development and comes without a
> production-readiness claim or support agreement. The license does not
> authorize testing against systems you do not own or have permission to test.

## Scope and context

Seev is a fintech reference/learning backend. Default Compose credentials
and mock vendor integrations are for local development only — they are not
production secrets and reporting them is not necessary. The repository's
own internal threat model, findings register, and severity assessments are
public at [docs/security/threat-model.md](docs/security/threat-model.md);
check there first, since a gap you've found may already be a tracked,
accepted-risk finding with a written rationale rather than an unknown
issue.

## Reporting a vulnerability

**Do not open a public issue for a security vulnerability.**

Please report it privately through
[GitHub Security Advisories](https://github.com/herdifirdausss/seev/security/advisories/new)
for this repository ("Report a vulnerability" under the Security tab).
This keeps the report confidential between you and the maintainer until a
fix is ready, and lets us credit you in the published advisory if you'd
like.

Include, as far as you're able:

- The affected file(s)/endpoint(s) and, ideally, a minimal reproduction.
- The realistic impact (what an attacker could actually do — money
  movement, PII exposure, privilege escalation, etc.).
- Whether it's exploitable against the default local Compose setup, or
  only in a local production-like test environment.

## What to expect

This is a solo-maintained project, not a funded security team — response
times are best-effort, not SLA-backed. You should still expect an
acknowledgment before a fix ships, and a note in
[docs/security/threat-model.md](docs/security/threat-model.md)'s findings
register once it's triaged, mirroring how internal findings (`TM-nn`) are
already tracked there.

## Financial invariants worth knowing before you report

Some things that look like bugs are enforced invariants documented in
[Project guide](docs/development/project-guide.md#financial-invariants) — e.g.
`ledger_entries` being genuinely append-only, or fail-open/fail-closed
behavior being an explicit, tested contract rather than an oversight.
Worth a skim before assuming something is unintentional.
