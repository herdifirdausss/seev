# First Kubernetes deployment feature scope

The first deployment proves the platform and synthetic money journeys. It is
not a production-vendor or observability rollout.

Enabled:

- all nine application services;
- PostgreSQL, Redis, RabbitMQ, and the migration Job;
- Gateway and Auth public API routes;
- VendorService mock callbacks;
- private Admin BFF and internal health/metrics;
- service workers needed for local correctness;
- Traefik, Calico policy, and the K6 proxy fixture.

Disabled or deferred:

- real vendor hosts and callbacks;
- merchant B2B public feature;
- privacy export and closure saga;
- backup scheduler and full observability;
- production credentials, data, and cloud resource creation.

All journeys use synthetic users, synthetic money, mock vendors, and disposable
volumes. The complete flag, route, worker, and exclusion contract is in
[first-deployment-scope.yaml](../../deploy/inventory/first-deployment-scope.yaml).
