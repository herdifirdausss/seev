# 02 — Riset Fitur: Ledger Fintech Kelas Dunia, Prioritas P0–P3

Referensi praktik: Modern Treasury, Stripe (ledger internal), TigerBeetle, Formance, Square, Uber (Gulfstream/LedgerStore). Kolom "Status di repo" menunjukkan seberapa jauh kode yang ada sudah mengarah ke sana.

## P0 — MVP (Phase 1). Tanpa ini bukan ledger.

| Fitur | Detail | Status di repo |
|---|---|---|
| Double-entry, append-only | Σdebit = Σcredit per transaksi; entries immutable, koreksi via reversal | ✅ engine + trigger DB (perlu migrasi kanonik) |
| Transaksi atomik multi-leg | Movement + fee dalam satu tx DB, satu outbox event | ✅ inline fee design di processors |
| Idempotency key (+scope) | Retry aman, duplicate → hasil pertama | ✅ engine; ⚠️ unique index perlu COALESCE (D4) |
| Concurrency-safe | FOR UPDATE urutan deterministik, retry + jitter, saldo ≥ 0 di DB | ✅ engine; ⬜ perlu test beban konkuren |
| Chart of accounts | user (cash/hold/pending/frozen/pocket) + system (settlement/fee/escrow/chargeback/confiscated/adjustment), qualifier per gateway/currency | ⚠️ konstanta ada; ⬜ provisioning + implementasi AccountRepository belum |
| Saldo available vs hold vs pending | Sebagai AKUN terpisah, bukan flag | ✅ desain account types |
| Presisi uang | BIGINT minor unit + decimal.Decimal | ✅ |
| Tipe transaksi inti | money_in, money_out, transfer_p2p | ✅ processors; ⬜ belum bisa dipanggil dari luar |
| HTTP API | post transaction, get balance, get tx, list entries (cursor) | ⬜ belum ada |
| Transactional outbox → broker | Event ditulis se-transaksi dengan posting; relay worker publish ke RabbitMQ | ⚠️ insert ada; ⬜ relay worker belum |
| Verifikasi invariant (trial balance) | Job harian: Σledger = 0 per tx; stored balance = Σentries per akun | ⚠️ fungsi SQL ada di draft; ⬜ job belum |
| Audit trail | created_by, error_message, header failed tetap di-commit | ✅ sebagian |

## P1 — Phase 2. Segera setelah MVP.

| Fitur | Detail | Status di repo |
|---|---|---|
| Hold / authorize–capture | initiate → settle/cancel; dasar escrow & card auth | ✅ processors withdraw/escrow lifecycle |
| Reversal / refund / chargeback | Selalu merujuk transaksi asal; partial refund menyusul | ✅ processors; ⬜ guard "reversal atas reversal" perlu test |
| Fee engine | Inline fee (sudah) + konfigurasi fee rule per tipe/gateway | ⚠️ mekanisme ada, rule engine belum |
| Freeze / confiscate | Compliance flow + alasan + audit | ✅ processors |
| Rekonsiliasi eksternal | Ledger vs settlement report gateway/bank; akun suspense untuk selisih | ⬜ |
| Daily balance snapshot | Closing balance per akun per hari; saldo as-of tanpa full scan | ⬜ |
| Kontrak event versioned | Skema payload event stabil (`ledger.transaction.posted.v1`) untuk modul lain | ⬜ |
| Statement / export | Rekening koran per akun, CSV export | ⬜ |

## P2 — Phase 3. Scale & kebutuhan bisnis.

| Fitur | Detail |
|---|---|
| Multi-currency + FX | Posting lintas currency via akun konversi; rate tersimpan sebagai fakta di transaksi |
| Limits & velocity | Limit per-tx/harian/bulanan di policy layer (BUKAN di ledger) |
| Maker-checker | `adjustment_*` wajib approval dua orang sebelum diposting |
| Scheduled/batch posting | Transaksi terjadwal, bulk disbursement (pakai `pkg/scheduler`) |
| Hot account mitigation | System accounts sudah di-shard per gateway (desain ada di processors.go); lanjutan: entry batching / async balance untuk akun super-hot |
| Partisi & archival | `ledger_entries` partisi bulanan (panduan migrasi sudah tertulis di skema lama); retensi & archive |
| Pagination & read replicas | Read path (statement, balance history) ke replica |

## P3 — Kelas dunia / compliance penuh.

| Fitur | Detail |
|---|---|
| AML / fraud hooks | Hook point pre-posting (sanctions, velocity anomali) — integrasi eksternal |
| Regulatory reporting | Posisi dana untuk regulator (BI/OJK); laporan berkala otomatis |
| Interest / yield accrual | Bunga harian produk saving; posting accrual otomatis |
| Point-in-time rebuild | Replay seluruh state dari entries; disaster recovery drill |
| Multi-region / HA | Failover, RPO/RTO target, outbox exactly-once semantics review |

## Yang Secara Sadar BUKAN Fitur Ledger (jangan dibangun di modul ini)

- Rate limit & daily limit per user → API/policy layer (komentar di processors.go sudah menyatakan ini).
- FX conversion execution → orchestration layer (money_out + money_in via FX service).
- User management, KYC, login → modul `auth` terpisah.
- Notifikasi (email/push) → modul `notification`, konsumen event outbox.
