# Software-supply-chain acceptance

## Scope

This document closes the repository-side controls for the world-class
engineering plan's supply-chain action. It covers source dependency and action
pinning, Go and container vulnerability gates, SBOMs, provenance, image
identity, and hardened production application runtimes.

The protected-registry run is intentionally separate. Repository configuration
cannot create registry credentials, an OIDC trust policy, or a deployed
admission verifier. Those items remain an explicit environment evidence gate.

## Repository controls

| Control | Status | Repeatable evidence |
|---|---|---|
| Go dependency integrity and vulnerability scanning | [x] | `go mod verify`, pinned `govulncheck` in `Makefile`, and the `lint-and-test` CI job |
| GitHub Action pinning | [x] | `scripts/ci/check-action-pins.sh` requires a full commit SHA and version comment |
| Immutable Dockerfile bases | [x] | `scripts/ci/supply-chain-check.sh` checks every repository Dockerfile `FROM` and Dockerfile frontend digest |
| BuildKit SBOM generator | [x] | Release `--attest=type=sbom` names a digest-pinned `docker/buildkit-syft-scanner` generator |
| Container vulnerability scanning | [x] | CI scans all nine loaded application images; release verification and protected publish scan the release digest with Trivy; HIGH/CRITICAL findings fail the gate |
| SBOM generation and retention | [x] | CI retains CycloneDX SBOMs for each application image; release jobs retain CycloneDX SBOMs and BuildKit SBOM metadata/attestations |
| Build provenance | [x] | Release Buildx commands request `mode=max` provenance and retain image metadata |
| Signing and verification workflow | [x] | Protected publish installs pinned Cosign, signs the immutable digest, verifies the GitHub OIDC identity, and checks OCI attestation descriptors |
| Minimal/non-root application images | [x] | Core and migration images use digest-pinned distroless `nonroot`; specialized backup and analytics images assert `postgres`/`kafka` runtime users |
| Read-only runtime | [x] | Helm application and migration templates enforce `readOnlyRootFilesystem`, dropped capabilities, no privilege escalation, `RuntimeDefault`, and non-root identity; `/tmp` is an explicit `emptyDir` |
| Release artifact retention | [x] | CI scan artifacts retain 30 days; release verification and protected-publish bundles retain 90 days |

## Verification

```sh
./scripts/ci/check-action-pins.sh
./scripts/ci/supply-chain-check.sh
go mod verify
```

After a real protected-release run, unpack the uploaded artifact and validate
its contents with:

```sh
make supply-chain-evidence-check EVIDENCE_DIR=/path/to/release-publish
```

The evidence verifier checks the immutable digest binding, zero HIGH/CRITICAL
Trivy findings, CycloneDX structure, non-empty Cosign verification output, the
OCI attestation manifest, and both SPDX SBOM and SLSA provenance layers.

The CI workflow is the executable verification path. It builds images from
the commit under test, checks revision labels, scans the local images, writes
JSON vulnerability reports and CycloneDX SBOMs, runs the container smoke test,
and uploads the evidence even when a preceding step fails.

The release workflow additionally builds the gateway image with SBOM and
provenance attestations, scans both the locally loaded release image and the
published immutable digest, signs it keylessly with Cosign, verifies the
workflow certificate identity, and checks that the registry exposes both
attestation descriptors.

## Protected-release execution

A repository administrator must first merge the workflow to the default branch,
configure the `protected-release` Environment with required reviewers and the
`RELEASE_REGISTRY`/`RELEASE_IMAGE_NAME` variables plus registry credentials,
then push a semver tag. Download the `release-publish-<run-id>-<attempt>`
artifact, unpack it, and run:

```sh
make supply-chain-evidence-check EVIDENCE_DIR=/path/to/unpacked-bundle
```

The verified bundle, tag, commit, image digest, and deployment/admission result
belong in the release record.

## Protected-environment evidence gate

- [ ] A real semver tag run completed with `RELEASE_REGISTRY` and
      `RELEASE_IMAGE_NAME` configured, and the `protected-release` GitHub
      Environment has required reviewers enabled.
- [ ] The registry accepted the OIDC-backed Cosign signature and the retained
      artifact contains `cosign-verify.json`.
- [ ] The retained artifact contains the image digest, Trivy report, SBOM,
      provenance/SBOM descriptor verification, and promotion decision.
- [ ] The deployed admission or runtime verifier rejected an unsigned or
      digest-mismatched image in a controlled test.
- [ ] Security/platform owners signed the release record.

Until those artifacts exist, the repository implementation is complete but
protected-registry signing, live attestation verification, and runtime
admission behavior remain `evidence_required`.
