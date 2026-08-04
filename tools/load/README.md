# Load tools

The packages under `tools/load/` support disposable, profile-bound capacity
runs. They are developer and CI tooling, not runtime service dependencies.

```text
tools/load/
├── lab/       # Validate a locked load profile and run manifest
└── report/    # Redacted report aggregation and validation
```

Use `make load-lint` for safe profile checks. Keep load data and reports under
the load-test workspace; never point a load command at shared development or
production data.
