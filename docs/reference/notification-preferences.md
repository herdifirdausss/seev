# Notification Preferences and Devices

Settings and preferences are user-scoped Gateway state. They never change the
Ledger event or the in-app mandatory channel.

## Settings

`/api/v1/notification-settings` accepts optimistic `version` updates for:

- `locale`: `en-US` or `id-ID`;
- IANA `timezone`;
- optional quiet hours (`HH:MM` start/end), including cross-midnight ranges;
- `daily_digest_hour` as `HH:MM`.

The planner applies quiet hours when creating immediate external work, and the
worker checks them again immediately before provider dispatch. This closes the
race where a user changes settings after planning.

## Preferences

Each preference selects a `category`, `channel`, and `mode`:
`immediate`, `daily_digest`, or `disabled`. `daily_digest` is valid only for
email and digest-eligible kinds. In-app is forced to `immediate`; the API
rejects attempts to disable or digest it. Feature flags and channel controls
are applied to the effective response and again at planning/dispatch time.

## Push devices

`POST /api/v1/notification-devices` accepts `android`, `ios`, `web`, or `test`
and a bounded token. Gateway stores the token only as encrypted ciphertext,
with a key version, HMAC fingerprint, and four-character suffix for safe
operator identification. The plaintext token is never returned, logged, put in
metrics, or exported.

The fingerprint is unique per user. A cross-user collision is rejected; a
same-user re-registration re-seals the token and reactivates the endpoint.
Invalid provider responses mark an endpoint invalid, and revocation prevents
future planning and dispatch.
