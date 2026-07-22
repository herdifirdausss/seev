# syntax=docker/dockerfile:1
#
# Pinned PostgreSQL 16 + pgBackRest image for docs/plan/50 (Track A7).
# Built once so the exact PostgreSQL and pgBackRest versions used for backup
# and restore are explicit and reproducible, never resolved from a floating
# tag at drill time (K2). The base image is pinned by full digest, not just
# a version tag, so the Alpine package repository snapshot pgbackrest
# resolves against never silently drifts across rebuilds of this Dockerfile.
#
# pgBackRest is MIT-licensed (github.com/pgbackrest/pgbackrest/blob/main/LICENSE,
# verified docs/plan/50 T1 execution). The exact resolved version is recorded
# in docs/plan/50-a7-backup-pitr-disaster-recovery.md's T1 Result section —
# see that file, not this comment, as the pin loses meaning if the two drift.
FROM postgres:16.14-alpine@sha256:57c72fd2a128e416c7fcc499958864df5301e940bca0a56f58fddf30ffc07777

# Hard-pinned to the exact version resolved and recorded at T1 execution
# time (see docs/plan/50 T1 Result) rather than a floating `apk add
# pgbackrest` — a later Alpine repository update must not silently change
# which pgBackRest release backs this repository's backups.
#
# Known Alpine packaging quirk, verified harmless: this Alpine release's
# `pgbackrest` package depends on the generic, unversioned `postgresql`
# meta-package, which resolves to PostgreSQL 18 (not 16) client tools —
# Alpine 3.24 no longer packages a postgresql16 apk at all. This installs
# postgresql18's binaries under /usr/bin (via /usr/libexec/postgresql/*
# symlinks) alongside the base image's OWN genuine PostgreSQL 16 binaries
# under /usr/local/bin. pgBackRest itself only actually links against
# `libpq.so.5` (version-agnostic soname); it does not require the
# postgresql18 client tools to function against a v16 cluster. The
# explicit PATH below guarantees /usr/local/bin (PostgreSQL 16) is always
# resolved first for any tool that shells out to `psql`/`pg_ctl`/`initdb`
# by bare name — verified by `which pg_ctl initdb` returning
# /usr/local/bin/* after this install, not the shadowed v18 copies.
RUN apk add --no-cache pgbackrest=2.58.0-r0
ENV PATH="/usr/local/bin:${PATH}"

# pgBackRest's default lock-path (/tmp/pgbackrest) auto-creates itself the
# first time ANY invocation touches it — if that first invocation happens
# to run as root (e.g. an operator's `docker compose exec` before
# archive_command ever fires), the directory ends up root-owned and
# archive_command (which always runs as the `postgres` OS user, the
# server process's own owner) gets a silent permission-denied failure on
# every WAL archive attempt. Pre-creating it with the right ownership here
# means the first-touch race can never decide who owns it.
RUN mkdir -p /tmp/pgbackrest && chown postgres:postgres /tmp/pgbackrest && chmod 770 /tmp/pgbackrest

# Wraps the base image's own docker-entrypoint.sh so the pgBackRest
# repository passphrase reaches archive_command's pgbackrest invocations as
# an environment variable rather than a static config-file value — see
# entrypoint.sh's own comment for why. The base image's ENTRYPOINT/CMD
# (docker-entrypoint.sh / postgres) are otherwise unchanged.
COPY entrypoint.sh /usr/local/bin/seev-backup-entrypoint.sh
RUN chmod +x /usr/local/bin/seev-backup-entrypoint.sh
ENTRYPOINT ["seev-backup-entrypoint.sh"]
CMD ["postgres"]
