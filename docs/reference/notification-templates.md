# Notification Templates

Templates are versioned Gateway data, selected by kind, channel, and locale.
Each delivery stores the rendered subject/title/body, content hash, locale, and
template version used at planning time; retry never re-renders against a newer
version.

## Context and safety

The renderer accepts only the typed `RenderContext`:

- canonical minor-unit amount, currency, and formatted display amount;
- transaction ID and timestamp;
- generated notification ID and an internal deep link.

Raw events, arbitrary maps, credentials, recipient addresses, and provider
fields are not template variables. Text and HTML use missing-key errors, bounded
output sizes, and a small helper allowlist. HTML uses `html/template`; in-app
and push templates cannot contain HTML. Subject/header CR/LF and unsafe deep
links are rejected.

The migration seeds v1 templates for `en-US` for all five money kinds and the
daily digest. The renderer also has deterministic built-ins for the initial
`en-US` and `id-ID` locales so an expand-only deployment cannot remove
mandatory in-app delivery. Database templates remain the governed source when
an active version exists.

## Maker/checker lifecycle

`draft -> pending_approval -> active -> retired` is the publish path;
`pending_approval -> rejected` is the rejection path. A checker cannot be the
same actor as the maker. Active versions are immutable, and the database
enforces one active version per kind/channel/locale.

Admin template fixtures render with a bounded sample context before a draft is
created. See [notification delivery](notification-delivery.md) for missing
template blocking and replay behavior.
