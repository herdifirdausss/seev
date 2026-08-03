# Storage and volume matrix

| Volume or store | Owner | Writable data | Persistence | First deployment |
|---|---|---|---|---|
| PostgreSQL data | data platform | nine owner databases | local disposable Compose volume; cloud stateful contract downstream | local / K2 |
| Object-store data | auth | synthetic KYC and export objects | local disposable volume | mock only |
| Redis | shared runtime | cache, leases, velocity, rate limits | ephemeral/non-authoritative | enabled |
| RabbitMQ | messaging | durable queues and DLQs | broker-managed; no K0 payload archive | enabled |
| Service certificates | platform security | none at runtime | injected read-only secret material | enabled |
| /tmp | each service | UNKNOWN temporary files | ephemeral | enabled with K1 review |
| Backup repository | operations | encrypted backups | external and UNKNOWN | deferred |
| Observability volumes | observability | metrics/log/trace data | separate optional profile | deferred |

The current Compose certificate directory is shared for convenience; this is a
security risk. Kubernetes must mount one service identity per workload and must
not make private keys part of the K0 evidence. Details and source references
are in [data-stores.yaml](../../deploy/inventory/data-stores.yaml).
