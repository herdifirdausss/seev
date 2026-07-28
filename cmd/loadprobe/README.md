# loadprobe

`loadprobe` reads only PostgreSQL monitoring views in a disposable load
database and emits redacted JSONL samples. It records query IDs, not query
text or parameters. Example:

```bash
go run ./cmd/loadprobe \
  -dsn 'host=127.0.0.1 port=15433 user=seev dbname=seev_load_ledger sslmode=disable' \
  -interval 100ms -duration 60s -out artifacts/load/<run>/postgres.jsonl
```
