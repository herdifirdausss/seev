# Image and runtime matrix

The current service image is a multi-binary build, selected by the SERVICE
build argument. K0 records the behavior so K1 can decide whether to retain or
split it.

| Property | Current behavior | K1 consequence |
|---|---|---|
| Builder | golang:1.26.5-alpine@sha256:0178a641fbb4858c5f1b48e34bdaabe0350a330a1b1149aabd498d0699ff5fb2 | digest-pinned builder; rebuild per selected service |
| Runtime | gcr.io/distroless/static-debian12:nonroot@sha256:f5b485ea962d9bd1186b2f6b3a061191539b905b82ec395de78cbfae51f20e35 | minimal image with no shell/package manager for debugging |
| User | nonroot | writable paths must be explicit |
| Binary | /app/service | one process per Pod |
| Entrypoint | /app/service | no hidden supervisor |
| Build selection | all service binaries built; selected SERVICE copied | build inefficiency; optimize only with evidence |
| Migrations | copied into every service image | K1 migration-image decision |
| Exposed ports | all application HTTP/gRPC ports in Dockerfile | EXPOSE is documentation, not exposure |
| Filesystem | read-only-compatible except declared temporary/object paths | K3 readOnlyRootFilesystem review |
| Certificates | injected at runtime | per-workload secret mount |

The generated image metadata is in
[image-inventory.json](../evidence/k0/generated/image-inventory.json). It
contains metadata only and no secret values. Source references:
Dockerfile, docker-compose.yml, and
[services.yaml](../../deploy/inventory/services.yaml).
