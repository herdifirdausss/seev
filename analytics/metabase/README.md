# Metabase

Metabase is optional local UI infrastructure. The versioned dashboard manifests
are the source of truth; the UI uses `metabase_bi` with the `bi_readonly` role
and has no OLTP connection. Stale/failing analytics is visible and never blocks
CDC or money movement.
