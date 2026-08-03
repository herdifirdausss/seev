# dbt snapshots

C2 v1 uses explicit CDC source ordering and current-state views rather than a
second opaque snapshot mechanism. A future snapshot is allowed only when its
grain, delete semantics, retention, and rebuild behavior are documented here.
