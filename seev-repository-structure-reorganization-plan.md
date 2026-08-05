# Repository Structure Reorganization Plan

## Status

Completed on 2026-08-05. The implementation and verification evidence are
recorded in
[`docs/architecture/repository-structure-reorganization-evidence.md`](docs/architecture/repository-structure-reorganization-evidence.md).

## Objective

Reorganize the repository around deployable service ownership while keeping
domain-neutral infrastructure, wire contracts, operational tooling, and test
support in explicit top-level areas. The reorganization must preserve runtime
behavior and make package ownership and architectural boundaries mechanically
verifiable.

## Target layout

- `services/<service>/` owns each deployable service, including its `cmd/`,
  private `internal/` packages, migrations, tests, and service README.
- `internal/platform/` contains domain-neutral runtime infrastructure.
- `internal/testkit/` contains shared test infrastructure.
- `contracts/` contains HTTP, protobuf, event, schema, vendor, and client
  contracts; generated Go bindings live under `gen/go/`.
- `deploy/`, `operations/`, `scripts/`, `tests/`, and `tools/` contain
  deployment, recovery, automation, acceptance, and developer tooling.

## Work packages

1. Move deployable service code and migrations under the owning service root.
2. Move shared runtime and test infrastructure into explicit platform and
   testkit roots.
3. Move wire contracts and generated bindings into `contracts/` and `gen/go/`.
4. Update imports, build configuration, Docker/Compose, Helm, Terraform, CI,
   scripts, and documentation to use the new layout.
5. Add architecture, ownership, boundary, onboarding, and forbidden-path
   checks so the layout remains enforceable.
6. Re-run repository, build, static, documentation, contract, acceptance, and
   disposable runtime checks against the reorganized tree.

## Completion criteria

- `go list ./...` resolves the service-centric tree without legacy root source
  buckets.
- Architecture and boundary checks reject forbidden legacy paths and invalid
  package ownership.
- Build, static checks, documentation checks, contract checks, and onboarding
  checks pass.
- Container, host, business, capability, admin, privacy, merchant, and load
  acceptance paths have recorded evidence.
- Remaining production-scale, cloud, and human onboarding measurements are
  explicitly release-scoped rather than represented as repository completion.

## Verification commands

```sh
make architecture-check
make onboarding-check
make verify-static
```

The completed results and retained evidence are maintained in the linked
implementation evidence document.
