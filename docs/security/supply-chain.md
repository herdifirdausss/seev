# Supply-chain release controls

Every runtime change is built from the repository lockfiles and a pinned
toolchain. CI enforces full-commit-SHA pins for external GitHub Actions,
`go mod verify`, pinned Dockerfile bases, Go vulnerability scanning, Trivy
container scanning, and non-root images. The CI job retains a vulnerability
report and CycloneDX SBOM for every loaded application image. The release
workflow requests BuildKit SBOM and provenance attestations, scans the
published digest, and retains the metadata and SBOM artifacts for promotion.
Production application and migration Pods enforce non-root identity,
read-only filesystems, dropped capabilities, no privilege escalation, and
the `RuntimeDefault` seccomp profile.

The protected `protected-release` GitHub Environment additionally signs and
verifies the published digest through its configured OIDC/sigstore policy
before promotion. The complete acceptance checklist is in
[`docs/acceptance/supply-chain.md`](../acceptance/supply-chain.md).

The workflow is an implementation of the control, not proof that a release
has already been published. The release record must retain:

- commit SHA and source repository identity;
- image digest and registry subject;
- SBOM artifact and scanner result;
- signed provenance/attestation verification result;
- vulnerability exceptions with owner, expiry, and compensating control.

No signing key or registry credential belongs in this repository. Configure
the protected registry and OIDC/signing policy in the release environment.
