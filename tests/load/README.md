# B0 load scenarios

Scenario scripts use open arrival-rate executors. The canonical set is W1–W7:
P2P, signed VendorService webhook, payout, mixed flow, hotspot, resolver, and
ledger-size read ladder. W2 points at VendorService in the disposable Compose
profile; it still requires a seeded top-up reference and user adapter before it
can produce business-capacity evidence. W7 is a bounded read probe and must be
run against a separately prepared 100k/1m/5m ledger-size dataset.

Business scenarios must never contain real credentials, vendor secrets, or
personal data. `smoke.js` is a non-claiming health/bootstrap check used by the
load Compose profile. `scripts/load-test.sh smoke|run` starts k6, records the
manifest and lifecycle markers under the exact run artifact directory, and
tears down the disposable project unless `SEEV_LOAD_KEEP_STACK=1` is set.
Business scenarios additionally write their redacted k6 summary there.

The Compose profile mounts the repository's mTLS identities read-only for the
services and Prometheus. Run `make certs` once before the first load smoke or
business run; the command is idempotent and does not use production
credentials. The profile keeps Redis and RabbitMQ internal and uses dedicated
load-only host ports for Postgres and Prometheus, so a development stack may
remain running.

Before a business run, provide a disposable bearer token with
`SEEV_LOAD_TOKEN`. W1 also needs `SEEV_LOAD_TARGET_USER_ID`; W2 needs
`SEEV_LOAD_USER_ID` and `SEEV_LOAD_TOPUP_REFERENCE` for an already-created
intent. Without these values the runner refuses to start, so an unauthenticated
401 or an unseeded webhook cannot be reported as successful load traffic.
