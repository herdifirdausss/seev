# Analytics scripts

Scripts are intentionally explicit about destructive actions. Connector delete
does not drop source replication slots; slot deletion requires
`analytics/postgres/drop-replication.sh` and a separate confirmation. Warehouse
reset removes only the analytics Compose project and volumes.
