# 17 — Phase 3a: Policy Layer & Recovery Drill (S1, S9)

Prasyarat: 10–16 selesai (sudah ✅). Keputusan desain: [13 K-S](13-p1-backlog-review.md) butir S1 dan S9 — baca dulu, jangan re-litigasi. Kedua task **independen** (tidak ada dependensi antar keduanya maupun ke task lain) — boleh dikerjakan paralel atau urutan bebas; S9 lebih kecil, cocok sebagai pemanasan.

Aturan verifikasi 09 "Pelajaran Terverifikasi" berlaku penuh: S1 menyentuh jalur request posting (transport) + tabel baru → integration test + smoke test wajib; S9 menyentuh proyeksi saldo → wajib dibuktikan terhadap Postgres asli dengan data nyata dari posting engine, bukan fixture sintetis saja.

---

## T1 — Limits & velocity policy layer (08 S1, keputusan K-S S1)

**Tujuan**: limit per-user, per-tipe, dan velocity (harian/bulanan) yang jauh lebih granular daripada safety ceiling global `LEDGER_MAX_AMOUNT_PER_TX` (10-T5). Contoh kebijakan yang harus bisa diekspresikan: "transfer_p2p max 5jt/transaksi, 20jt/hari, 100jt/bulan per user"; "withdraw_initiate max 3x/hari per user".

**Batas arsitektur yang dikunci (K-S S1)**:
- Modul baru `internal/policy` — **ledger module tidak tahu-menahu**. Evaluasi terjadi di transport layer SEBELUM `ledger.Post`, persis seperti komentar yang sudah ditinggal di `internal/ledger/processors/processors.go` ("Limits (per-tx, daily, velocity) belong in your API/policy layer, NOT here").
- Counter velocity: Redis sliding-window BILA `REDIS_ENABLED=true`, in-memory fallback bila false — **pola persis 12-T1** (`pkg/cache/rate_limiter.go`: `RedisRateLimiter`/`MemoryRateLimiter` di belakang satu interface `Limiter`). JANGAN menciptakan mekanisme fallback ketiga.
- Konfigurasi limit di tabel DB, di-cache in-process dengan TTL (bukan hardcode, bukan env — ops harus bisa mengubah limit tanpa deploy).

### Langkah
1. Migrasi `000010_policy_limits.up.sql` (+down):
   ```sql
   CREATE TABLE policy_limits (
       id               UUID        PRIMARY KEY,
       -- NULL user_id = limit default per-tipe (berlaku semua user);
       -- baris dengan user_id spesifik meng-override default untuk user itu.
       user_id          UUID        NULL,
       transaction_type TEXT        NOT NULL,
       max_per_tx       BIGINT      NULL CHECK (max_per_tx  > 0),
       max_daily_amount BIGINT      NULL CHECK (max_daily_amount > 0),
       max_daily_count  INT         NULL CHECK (max_daily_count > 0),
       max_monthly_amount BIGINT    NULL CHECK (max_monthly_amount > 0),
       enabled          BOOLEAN     NOT NULL DEFAULT true,
       created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
       updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
       UNIQUE (user_id, transaction_type)
   );
   -- NULL user_id harus tetap unik per tipe → partial unique index (pola uq_ltx_idempotency)
   CREATE UNIQUE INDEX uq_policy_limits_default ON policy_limits(transaction_type) WHERE user_id IS NULL;
   ```
   Semua kolom limit NULLable — NULL = dimensi itu tidak dibatasi. Tambahkan grant di migrasi yang sama: `GRANT SELECT, INSERT, UPDATE ON policy_limits TO app_service;` + `GRANT SELECT TO app_readonly` + ENABLE/FORCE RLS + policy (ikuti pola persis 000009 — **setiap tabel baru pasca-000009 wajib membawa grant+RLS-nya sendiri**, jangan tunggu migrasi RLS kolektif kedua).
2. Package `internal/policy`:
   - `type Decision struct { Allowed bool; Rule string; Detail string }` — `Rule` menyebut dimensi yang dilanggar (`"max_per_tx"`, `"max_daily_amount"`, ...) untuk pesan error yang jelas.
   - `type Engine struct` dengan dependensi: `*database.DBSQL` (baca `policy_limits`), `cache.Limiter`-style counter (lihat langkah 3), `time.Location` (Asia/Jakarta — hari/bulan kalender harus konsisten dengan snapshot/statement, JANGAN pakai UTC di sini).
   - `func (e *Engine) Check(ctx, userID uuid.UUID, txType string, amount decimal.Decimal) (Decision, error)`:
     a. Ambil limit efektif: baris `user_id=<user>` kalau ada, fallback baris `user_id IS NULL`, fallback "tanpa limit" (MVP: tipe tanpa baris = tidak dibatasi — kebijakan opt-in per tipe, bukan default-deny; default-deny akan mematikan tipe internal yang belum dikonfigurasi).
     b. `max_per_tx`: bandingkan langsung.
     c. Velocity harian/bulanan: baca counter (lihat langkah 3) + amount berjalan; kalau `counter + amount > max` → tolak.
   - `func (e *Engine) Record(ctx, userID, txType, amount)` — dipanggil transport **hanya setelah `ledger.Post` sukses** (posting gagal tidak boleh memakan kuota). Ini berarti ada race kecil (dua request konkuren bisa dua-duanya lolos Check sebelum salah satu Record) — **terima**: limit velocity adalah kontrol bisnis kasar, bukan invariant uang; invariant uang tetap di ledger. Tulis batasan ini di doc comment.
3. Counter velocity di `pkg/cache` (extend, jangan buat package baru):
   - Interface baru `Counter` dengan `IncrBy(ctx, key string, delta int64, ttl time.Duration) (int64, error)` dan `Get(ctx, key string) (int64, error)`.
   - `RedisCounter` (INCRBY + EXPIRE NX, satu roundtrip via pipeline) dan `MemoryCounter` (map + mutex + janitor, pola persis `MemoryRateLimiter`).
   - Key: `pol:<userID>:<txType>:d:<YYYY-MM-DD>` (TTL 48h) dan `pol:<userID>:<txType>:m:<YYYY-MM>` (TTL 35 hari). Tanggal dihitung di `loc` Asia/Jakarta.
   - Dua nilai per window (amount dan count) → dua key (`...:amt`, `...:cnt`) — jangan encode dua nilai dalam satu value.
   - Pemilihan Redis vs memory terjadi di composition root (`cmd/server/main.go` / `internal/handler/dependencies.go`) berdasar `REDIS_ENABLED` — pola persis pemilihan `RedisLock`/`MemoryLock` di `internal/ledger/ledger.go:145-154`.
4. Cache konfigurasi limit in-process: `Engine` menyimpan hasil query `policy_limits` di `sync.Map` dengan TTL 60 detik (konstanta, bukan config). Invalidasi berbasis waktu saja — perubahan limit terasa maksimal 60 detik kemudian; itu cukup, jangan bangun pub/sub invalidation.
5. Wiring transport: di `internal/ledger/transport/http.go` `postTransaction`, SETELAH semua validasi bentuk (amount parse, tipe, metadata) dan SEBELUM `h.svc.Post`:
   ```go
   if h.policy != nil { // nil = policy layer tidak diwire (router internal — lihat bawah)
       dec, err := h.policy.Check(ctx, userID, req.Type, amount)
       ...tolak 422 kalau !dec.Allowed dengan pesan menyebut dec.Rule...
   }
   ```
   dan `h.policy.Record(...)` setelah Post sukses. **Router internal (`NewInternalRouter`) TIDAK diwire policy engine** (`policy: nil`) — panggilan internal (webhook gateway, ops) tidak tunduk limit user; yang tunduk hanya jalur publik. Interface kecil `PolicyChecker` didefinisikan di package transport (pola `Poster`/`AdjustmentCreator` — hindari import cycle), `internal/policy.Engine` memenuhinya secara struktural.
6. Endpoint admin CRUD limit di router internal (admin-gated, pola `/admin/adjustments`): `PUT /admin/policy/limits` (upsert by user_id+type), `GET /admin/policy/limits?type=&user_id=`. DELETE tidak perlu — set `enabled=false` (audit trail tetap ada; ini juga alasan grant `app_service` di langkah 1 tidak mencakup DELETE).
7. Error mapping: sentinel baru `apperror.ErrPolicyLimitExceeded = errors.New("POLICY_LIMIT_EXCEEDED")` → 422 di `transport/errors.go`. JANGAN 429 — 429 milik rate-limiter infrastruktur (per-IP); ini penolakan kebijakan bisnis.

### Test wajib
- Unit `internal/policy`: table-driven per dimensi (per-tx lewat/tolak, daily amount, daily count, monthly amount, user-override menang atas default, tipe tanpa baris = tidak dibatasi, `enabled=false` = tidak dibatasi).
- Unit counter: `MemoryCounter` TTL expiry + concurrent IncrBy (`-race`).
- Integration (Postgres asli): upsert limit via repo → Check membaca nilai baru setelah TTL cache lewat (sleep 61s TIDAK boleh — inject clock/TTL kecil di test).
- Integration transport: POST transfer_p2p melebihi max_per_tx → 422 dengan pesan menyebut rule; POST kedua yang menembus daily amount → 422; posting GAGAL (mis. saldo kurang) tidak memakan kuota (Record tidak terpanggil).
- Smoke test via curl (stack Docker penuh): set limit via admin endpoint → transfer pertama lolos → transfer kedua melampaui daily → 422.

### DoD
- [x] `internal/ledger` tidak menyentuh/meng-import `internal/policy` sama sekali (cek dengan `grep -r "internal/policy" internal/ledger/` = kosong).
- [x] Fallback in-memory terbukti: suite integration policy dijalankan dengan `REDIS_ENABLED=false` juga.
- [x] Migrasi up+down teruji; tabel baru membawa grant+RLS sendiri (query `pg_policies` membuktikan).

### Hasil (2026-07-12)
Implementasi mengikuti T1 dengan satu penyederhanaan sadar: `Check` TIDAK mengembalikan `Decision` struct seperti draf awal dokumen ini — melainkan `(allowed bool, rule string, detail string, err error)` — supaya `transport.PolicyChecker` (interface didefinisikan DI `internal/ledger/transport`, bukan diimpor dari `internal/policy`) tidak perlu tipe struct bersama antar dua module yang sengaja independen; kepuasan interface tetap murni struktural (Go tidak butuh caller mengimpor package pendefinisi tipe konkret).

- `migrations/000010_policy_limits.{up,down}.sql` — tabel `policy_limits` dengan override per-user (unique constraint biasa) + default per-tipe (partial unique index `uq_policy_limits_default` khusus `user_id IS NULL`, pola sama seperti `uq_ltx_idempotency`). Grant+RLS dibawa migrasi ini sendiri (pola pasca-000009).
- `pkg/cache/counter.go` — interface `Counter` baru (`IncrBy`/`Get`, window TTL kumulatif — beda bentuk dari `Limiter` yang sudah ada untuk rate-limit token-bucket) + `RedisCounter` (INCRBY + EXPIRE NX via pipeline) + `MemoryCounter` (map+mutex+GC, pola persis `MemoryRateLimiter`).
- `internal/policy/` (package baru): `repository.go` (CRUD `policy_limits`, `GetEffective` satu query dengan `ORDER BY user_id NULLS LAST LIMIT 1` untuk resolusi override), `policy.go` (`Engine.Check`/`Record`, cache limit in-process dengan TTL dapat diinjeksi via `WithCacheTTL` untuk test), `http.go` (admin CRUD `PUT`/`GET /admin/policy/limits`, admin-gated).
- **Interface `PolicyChecker` didefinisikan di `internal/ledger/transport`** (bukan diimpor dari `internal/policy`) — `internal/policy.Engine` memenuhinya secara struktural. `ledger.NewModule` menerima parameter baru `policyChecker` (alias re-export `ledger.PolicyChecker = transport.PolicyChecker`), diteruskan ke `transport.NewRouterWithPolicy` (fungsi baru; `NewRouter` lama tetap ada, memanggil varian baru dengan `nil`, byte-identik dengan sebelumnya). Dicek di `postTransaction`: `Check` sebelum `svc.Post`, `Record` HANYA setelah `Post` sukses. Router internal TIDAK pernah menerima policy checker (nil eksplisit) — pemanggil internal tepercaya tidak kena limit user.
- **Desain penting yang ditemukan lewat chaos test, bukan di draf awal**: `Check` awalnya mengembalikan error saat repository/counter infra gagal (fail-closed) — `./scripts/chaos-test.sh all` scenario 4 (Redis down) lolos kebetulan karena tidak ada limit dikonfigurasi di data chaos-test yang fresh, sehingga jalur counter tidak pernah tersentuh. Diperbaiki SEBELUM jadi bug produksi: `Check` sekarang **fail OPEN** pada error infra (log + izinkan), konsisten dengan pola `Limiter`/rate-limiter yang sudah ada dan sudah di-chaos-test eksplisit di codebase ini. `max_per_tx` (murni aritmetika, tidak butuh counter) tetap ditegakkan bahkan saat counter mati — dibuktikan test terpisah.
- Unit test: 20 di `internal/policy` (`policy_test.go`: Check per dimensi + user-override + cache TTL + fail-open pada repo/counter error; `http_test.go`: admin CRUD, admin-gate, validasi input) + 8 di `pkg/cache/counter_test.go` (`MemoryCounter` TTL expiry + concurrent IncrBy `-race`) + 6 di `internal/ledger/transport/policy_test.go` (wiring: policy allow→Record terpanggil, policy reject→Post tidak terpanggil, Post gagal→Record tidak terpanggil, router internal tidak pernah dicek, `NewRouter` tanpa policy = perilaku identik sebelum fitur ini ada).
- Integration test (`internal/policy/policy_integration_test.go`, Postgres asli, package terpisah — tidak bergantung pada `internal/ledger` sama sekali, sesuai independensi module): round-trip repository (override vs default, upsert kedua kali UPDATE bukan duplikat), **cache TTL kadaluarsa lalu re-fetch nilai baru dari DB** (TTL diinjeksi kecil, BUKAN sleep 61 detik), velocity harian end-to-end dengan counter nyata.
- **Bug nyata ditemukan & diperbaiki oleh integration test ini** (bukan lolos ke smoke test): `Upsert`'s `ON CONFLICT (user_id, transaction_type)` tidak pernah tersentuh untuk baris default (`user_id IS NULL`) karena constraint UNIQUE biasa tidak pernah menganggap dua NULL sebagai konflik menurut standar SQL — upsert kedua untuk default yang sama gagal dengan `duplicate key value violates unique constraint "uq_policy_limits_default"` alih-alih ter-UPDATE. Diperbaiki dengan dua statement terpisah (`ON CONFLICT (transaction_type) WHERE user_id IS NULL` untuk default, `ON CONFLICT (user_id, transaction_type)` untuk override) — Postgres hanya mengizinkan SATU arbiter index per `ON CONFLICT`, tidak bisa digabung.
- Smoke test manual via curl terhadap stack Docker penuh (server berjalan sebagai `seev_app`/`app_service`): set `max_per_tx=5000` → transfer 5000 sukses, 5001 ditolak 422 dengan pesan menyebut rule → tambah `max_daily_amount=6000` → transfer di bawah limit sukses, transfer yang melampaui ditolak 422 setelah cache 60 detik kadaluarsa (perilaku cache TTL production yang didokumentasikan, terverifikasi nyata) → counter Redis diverifikasi langsung (`amt=6001`, `cnt=3`, `TTL≈48h`) sesuai jumlah transfer sukses.
- `./scripts/chaos-test.sh all` dijalankan ULANG dengan pipeline posting yang kini menyertakan policy check (tanpa limit dikonfigurasi = jalur cepat "allowed=true", tidak ada regresi) — keempat skenario PASS.
- `go build ./...`, `go vet ./...` (termasuk `-tags=integration`), `go test -race ./...`, `go test -tags=integration -race ./...` — semua hijau. Migrasi 000010 up→down→up diuji bersih.

---

## T2 — Point-in-time rebuild & DR drill (08 S9, keputusan K-S S9)

**Tujuan**: bukti empiris (bukan klaim desain) bahwa `account_balances` adalah proyeksi murni yang bisa dibangun ulang penuh dari `ledger_entries`, dan bahwa prosedur restore-dari-backup punya langkah tertulis + RTO terukur.

**Peringatan desain**: `account_balances` menyimpan `allow_negative` yang BUKAN turunan dari entries (di-seed per tipe akun di 000002/000008, dan bisa menyimpang dari aturan tipe di masa depan). Karena itu rebuild = **UPDATE balance dari agregat, BUKAN `TRUNCATE` + re-insert** — truncate akan menghancurkan `allow_negative`. Ini penyesuaian sadar atas kata "truncate projection → replay" di 08/13; semantiknya sama (nilai balance 100% dari entries), mekanismenya lebih aman.

### Langkah
1. Script `scripts/rebuild-projection.sh` (pola struktur/logging `scripts/chaos-test.sh`):
   a. Pre-check: server TIDAK boleh berjalan (script menolak jalan kalau port app merespons /health — rebuild pada sistem live = race dengan posting engine; ini prosedur maintenance-window).
   b. Simpan snapshot pra-rebuild untuk perbandingan: `CREATE TEMP TABLE pre_rebuild AS SELECT account_id, balance FROM account_balances;`
   c. Rebuild satu statement set-based (bukan loop per akun):
      ```sql
      UPDATE account_balances ab
      SET balance = COALESCE(agg.computed, 0), updated_at = now()
      FROM (SELECT account_id,
                   SUM(amount) FILTER (WHERE direction='credit') -
                   SUM(amount) FILTER (WHERE direction='debit') AS computed
            FROM ledger_entries GROUP BY account_id) agg
      WHERE ab.account_id = agg.account_id
        AND ab.balance IS DISTINCT FROM COALESCE(agg.computed, 0);
      ```
      plus statement kedua untuk akun TANPA entries sama sekali (`balance = 0 WHERE NOT EXISTS ...`).
      Jalankan sebagai **role owner/migrate** (`POSTGRES_MIGRATE_USER`), bukan `app_service` — dan ini disengaja tetap bisa: RLS FORCE tidak menghalangi owner non-superuser? **Perhatian**: sejak 000009, owner non-superuser TERIKAT policy (FORCE). Kalau `POSTGRES_MIGRATE_USER` adalah bootstrap superuser (dev), bypass otomatis; di produksi kalau owner bukan superuser, script butuh policy yang mengizinkan — verifikasi di integration test langkah 4, dan bila perlu tambahkan policy `pol_all_service`-style untuk role owner di migrasi 000010.
   d. Verifikasi pasca-rebuild di script yang sama: `fn_verify_account_balance` per akun yang berubah HARUS konsisten; `v_account_balance_audit` bersih; laporkan diff pre/post (akun yang nilainya berubah = bukti proyeksi sempat menyimpang — di sistem sehat harus 0 baris).
   e. Exit code non-zero bila ada inkonsistensi pasca-rebuild.
2. Runbook `docs/runbooks/dr-restore-drill.md`: prosedur drill di staging — restore backup (pg_dump/pg_restore atau volume snapshot), jalankan `migrate up` (idempoten — versi sudah tersimpan), jalankan `rebuild-projection.sh`, jalankan verifier penuh, start server, smoke test posting, **catat RTO setiap drill di tabel di runbook itu sendiri** (tanggal, durasi per langkah, total). Kolom pertama diisi dari drill pertama yang dilakukan saat mengerjakan task ini terhadap stack Docker lokal.
3. Integration test `TestSchemaContract_RebuildProjection` di `schema_contract_test.go`: posting nyata beberapa transaksi (money_in + transfer + adjustment via maker-checker) → korupsi proyeksi secara sengaja via SQL (`UPDATE account_balances SET balance = balance + 999 WHERE ...` — sebagai owner) → jalankan SQL rebuild yang sama persis dengan script (extract ke file `.sql` yang dibaca script DAN test, supaya tidak ada dua salinan yang bisa menyimpang) → assert saldo kembali benar, `allow_negative` tidak berubah, verifier bersih.
4. Drill nyata satu kali terhadap stack Docker lokal sebagai bagian dari DoD task ini (bukan hanya "tertulis di runbook"): backup → hancurkan → restore → rebuild → verifikasi, dengan durasi tercatat.

### Test wajib
- Integration langkah 3 (satu file SQL dipakai script dan test).
- Script dijalankan manual terhadap stack Docker dengan data hasil smoke test 16 — laporan diff 0 baris (proyeksi sehat) DAN laporan diff >0 baris setelah korupsi disengaja.
- Negative: script menolak jalan saat server hidup.

### DoD
- [x] `scripts/rebuild-projection.sh` + `scripts/sql/rebuild_projection.sql` ada, idempoten, exit code benar.
- [x] `docs/runbooks/dr-restore-drill.md` berisi RTO drill pertama yang nyata.
- [x] `allow_negative` terbukti selamat dari rebuild (assertion eksplisit di integration test).

### Hasil (2026-07-12)
Implementasi persis mengikuti T2 dengan satu file SQL bersama (`scripts/sql/rebuild_projection.sql`) dibaca baik oleh `scripts/rebuild-projection.sh` maupun test Go — tidak ada dua salinan yang bisa menyimpang. Rebuild memakai UPDATE set-based (bukan TRUNCATE) persis sesuai peringatan desain di dokumen ini; integration test `TestSchemaContract_RebuildProjection` membuktikan `allow_negative` selamat via assertion eksplisit sebelum/sesudah pada akun user DAN akun sistem (settlement). Test kedua `TestSchemaContract_RebuildProjection_IdempotentNoOp` membuktikan `RowsAffected()==0` saat proyeksi sudah konsisten.

**Drill nyata dijalankan** terhadap stack Docker lokal (bukan hanya ditulis di runbook): seed data via posting sungguhan → `pg_dump` → `DROP DATABASE` (simulasi bencana) → `pg_restore` → `migrate up` (no-op, versi sudah tercakup di dump) → `rebuild-projection.sh` → verifikasi → start server → smoke test (saldo pra-insiden utuh + posting baru sukses). **Drill ini menemukan bug nyata**: script memakai `psql -f <path>` lewat `docker exec`, tapi path itu di-resolve di dalam filesystem KONTAINER, bukan host — jalur docker-exec tidak akan pernah menemukan file SQL-nya. Diperbaiki dengan pipe stdin (`docker exec -i ... psql ... < file`) — persis alasan drill ini wajib dijalankan nyata, bukan cuma didesain di atas kertas. Re-run setelah perbaikan sukses bersih. RTO dicatat di tabel runbook (dominan waktu debug, bukan mekanisme — dicatat apa adanya, bukan dipoles).

Test negatif (script menolak jalan saat app hidup) diverifikasi manual dengan health-check tiruan yang merespons 200 — exit code 1 terkonfirmasi.

`go build ./...`, `go vet ./...` (termasuk `-tags=integration`), `go test -tags=integration -race ./...` (kedua test) — semua hijau.

---

## Verifikasi akhir (kedua task)

```bash
go build ./... && go vet ./... && go vet -tags=integration ./...
make test
go test -tags=integration -race ./...
./scripts/chaos-test.sh all        # T1 mengubah jalur request publik
```
Smoke test manual via curl untuk endpoint baru (pola sesi 10–16: remap port Postgres bila 5432 terpakai, kembalikan setelah selesai). Setelah selesai: update checkbox DoD + tulis bagian "Hasil" di dokumen ini, update status di [README.md](README.md), dan tandai item S1/S9 di [08-phase-3-scale.md](08-phase-3-scale.md) sebagai superseded oleh dokumen ini.
