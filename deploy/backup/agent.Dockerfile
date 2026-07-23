# syntax=docker/dockerfile:1
#
# docs/roadmap/active/50-a7-backup-pitr-disaster-recovery.md T2 (K13): backup-agent needs BOTH a compiled Go binary and
# a local pgbackrest install with direct filesystem access to PGDATA — it
# runs pgBackRest's "backup" command itself, sharing seev_postgres_data
# (read-only) and the Postgres unix-socket directory with the postgres
# container via named volumes (docker-compose.yml). pg1-host is
# deliberately never set in deploy/backup/pgbackrest.conf: from
# pgBackRest's own point of view PGDATA and the control socket are just
# local paths, so it never needs its own SSH/TLS remote-protocol mode —
# that split-host complexity is real scope docs/roadmap/active/50-a7-backup-pitr-disaster-recovery.md §8 explicitly
# does not take on for this single-Compose-host lab environment.
#
# Builder stage matches the root Dockerfile's own Go build exactly
# (same base image/version, same trimpath+ldflags). Build context must be
# the repo root (not ./deploy/backup) so this stage can see go.mod/cmd/
# internal/pkg.
FROM golang:1.25.12-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/backup-agent ./cmd/backup-agent

# Final stage intentionally repeats deploy/backup/Dockerfile's own
# base-image digest and pgbackrest package pin verbatim (rather than
# building FROM that image, which would create a Compose build-order
# dependency) — see that file's comment for why each pin is exactly what
# it is; the two must be kept in sync by hand if either ever changes.
FROM postgres:16.14-alpine@sha256:57c72fd2a128e416c7fcc499958864df5301e940bca0a56f58fddf30ffc07777
RUN apk add --no-cache pgbackrest=2.58.0-r0
ENV PATH="/usr/local/bin:${PATH}"
RUN mkdir -p /tmp/pgbackrest && chown postgres:postgres /tmp/pgbackrest && chmod 770 /tmp/pgbackrest

COPY --from=builder /out/backup-agent /usr/local/bin/backup-agent

# docs/roadmap/archive/44-a2-ci-pipeline.md K5's REVISION build arg convention (root Dockerfile) —
# baked in at build time so manifest.go can report which commit this
# image was built from without needing a .git directory inside the
# container (there isn't one).
ARG REVISION=unknown
ENV GIT_COMMIT=${REVISION}

# Runs as the same `postgres` OS user the postgres container's own
# archive_command/manual pgbackrest invocations use — required so the
# shared read-only seev_postgres_data mount (owned postgres:postgres) and
# the shared socket-directory volume are both actually readable here.
USER postgres
ENTRYPOINT ["backup-agent"]
