# Port and protocol matrix

Container listeners bind broadly in the current Docker image; exposure is a
deployment decision. Host Compose mappings are loopback-only local test
surfaces. The complete listener contract, including bind, TLS, authentication,
and downstream owner, is in
[ports.yaml](../../deploy/inventory/ports.yaml).

| Service | Listener | Port | Protocol | TLS / mTLS | Exposure class |
|---|---|---:|---|---|---|
| gateway | public | 8080 | HTTP | edge termination / no mTLS | PUBLIC_EDGE |
| gateway | internal | 8081 | HTTPS | service leaf / mTLS | INTERNAL_HTTP |
| auth | public | 8082 | HTTP | edge termination / no mTLS | PUBLIC_EDGE |
| auth | internal | 8083 | HTTPS | service leaf / mTLS | INTERNAL_HTTP |
| ledger | user and internal | 8090, 8091 | HTTPS | service leaf / mTLS | private/internal |
| ledger | gRPC | 9091 | gRPC | service leaf / mTLS | INTERNAL_GRPC |
| payin | admin/internal | 8092 | HTTPS | service leaf / mTLS | PRIVATE_ADMIN |
| payin | gRPC | 9092 | gRPC | service leaf / mTLS | INTERNAL_GRPC |
| payout | admin/internal | 8093 | HTTPS | service leaf / mTLS | PRIVATE_ADMIN |
| payout | gRPC | 9093 | gRPC | service leaf / mTLS | INTERNAL_GRPC |
| fraud | admin/internal | 8094 | HTTPS | service leaf / mTLS | PRIVATE_ADMIN |
| fraud | gRPC | 9094 | gRPC | service leaf / mTLS | INTERNAL_GRPC |
| admin-bff | console | 8095 | HTTPS | service leaf / mTLS | PRIVATE_ADMIN |
| assurance | internal | 8096 | HTTPS | service leaf / mTLS | PRIVATE_ADMIN |
| assurance | gRPC | 9096 | gRPC | service leaf / mTLS | INTERNAL_GRPC |
| vendor | callback/admin | 8098 | HTTPS | service leaf / mTLS | CALLBACK_EDGE |
| vendor | gRPC | 9098 | gRPC | service leaf / mTLS | INTERNAL_GRPC |
| PostgreSQL | data | 5432 | PostgreSQL | local Compose default | DATA_PRIVATE |
| Redis | data | 6379 | Redis | local Compose default | DATA_PRIVATE |
| RabbitMQ | AMQP / management | 5672 / 15672 | AMQP / HTTP | local Compose default | data / local tool |
| Squid | egress proxy | 3128 | HTTP CONNECT | destination ACL | INTERNAL_HTTP |

Health, readiness, metrics, gRPC, database, broker management, and proxy
listeners are never public edge routes. Source references are the listener
registrations in each cmd/*/main.go, Dockerfile, Compose, and the
[generated baseline commands](../evidence/k0/command-output/).
