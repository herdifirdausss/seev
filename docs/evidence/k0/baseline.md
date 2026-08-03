# K0 baseline evidence

Captured: 2026-08-03T12:20:06Z

## Pinned baseline

- Commit: 1fa942950145b368fd46058a315ddc163275625d
- Branch: main
- Operator: oyherdifirdaus
- Machine OS/architecture: Darwin 24.6.0 x86_64
- CPU count: 8
- Host memory: 8.00 GiB (raw value is intentionally not repeated)
- Filesystem: 26Gi available of 228Gi
- Go: see [go-version.txt](command-output/go-version.txt)
- Docker engine: see [docker-version.txt](command-output/docker-version.txt) and [docker-info-safe.txt](command-output/docker-info-safe.txt)
- Docker Compose: see [docker-compose-version.txt](command-output/docker-compose-version.txt)

## Working tree rule

The repository was already modified by the completed Plan 63 implementation
before Plan 64 began. The pre-K0 snapshot is preserved in
[baseline-pre-k0.status](baseline-pre-k0.status). The current status is in
[git-status-current.txt](command-output/git-status-current.txt). K0 does not
silently treat either state as clean.

## Test-data and evidence policy

- Use synthetic users, synthetic money, mock vendors, and disposable local
  volumes only.
- Record configuration names, not secret values.
- Do not archive .env files, private keys, tokens, database dumps, or raw user data.
- Normalized Compose output is redacted before it is committed.
- This evidence authorizes no Kubernetes or cloud mutation.

## Reproduction

~~~sh
make k0-inventory
make k0-inventory-check
~~~

Source hierarchy: running behavior and tests, application code, Dockerfile and
Compose, API contracts, configuration validation, current docs, then archived
roadmap prose.
