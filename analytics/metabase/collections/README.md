# Metabase collections

Create two collections from the versioned dashboard manifests:

- `C2 / Business`: executive overview, Pay-in, Payout, fee conversion, and
  modeled unit economics.
- `C2 / Operations`: data-platform health.

Metabase receives only the `bi_readonly` ClickHouse credential. It has no
PostgreSQL connection, no raw/staging permissions, no ClickHouse write grant,
and no user-level identity filters. Dashboard SQL must target `mart.*`, the
approved core dimensions, or the two approved control summaries.
