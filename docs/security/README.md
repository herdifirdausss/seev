# Security Documentation

> [Documentation home](../README.md) · **Security**

> **Safety boundary:** Seev is open source under
> [Apache-2.0](../../LICENSE), but the license is not a production-readiness
> claim and does not authorize testing systems without the owner's permission.

- [Threat model](threat-model.md) describes assets, trust boundaries, threats,
  controls, and accepted local-development risks.
- [Database and authorization acceptance](../acceptance/security.md) indexes
  the repository-side controls and repeatable security-contract checks.
- [Software-supply-chain acceptance](../acceptance/supply-chain.md) indexes
  dependency/action/image pins, scan/SBOM/provenance gates, and the protected
  release evidence boundary.
- [Independent security review scope](independent-review-scope.md) defines the
  assessor scope, evidence contract, finding format, and exit criteria; the
  [acceptance packet](../acceptance/independent-security-review.md) is the
  retained-evidence index.
- [Security policy](../../SECURITY.md) explains how to report a vulnerability
  privately. Do not disclose an unpatched vulnerability in a public issue.
- Security incident procedures live in the
  [runbook index](../operations/runbooks/README.md).

Target security designs remain in the [active roadmap](../roadmap/README.md)
until their acceptance evidence is complete.
