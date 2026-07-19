# Product assurance dan intake control

Assurance-service (`8096`/`18096`) hanya membaca payin, payout, dan ledger
melalui gRPC. Ia tidak mengubah transaksi domain. Finding critical/high baru,
reopen, atau escalation severity masuk durable alert queue.

## Pemeriksaan normal

```bash
ASSURANCE_TOKEN="$TOKEN" scripts/product-assurance.sh summary
ASSURANCE_TOKEN="$TOKEN" scripts/product-assurance.sh list 'status=open&severity=critical'
ASSURANCE_TOKEN="$TOKEN" scripts/product-assurance.sh run
```

Jika run gagal karena dependency unavailable, cursor tidak boleh maju. Periksa
`/admin/assurance/runs` dan metric `assurance_run_failures_total`; setelah
dependency pulih, jalankan manual run atau tunggu interval 60 detik.

## Menangani finding

1. Acknowledge dengan alasan investigasi; acknowledge tidak menghilangkan
   money-at-risk.
2. Pulihkan akar masalah di owner service melalui prosedur domain yang ada.
3. Resolve hanya setelah proof berikutnya sehat. Finding tetap tersimpan dan
   akan reopen bila mismatch yang sama muncul lagi.

```bash
scripts/product-assurance.sh acknowledge <finding-id> "investigating webhook lag"
scripts/product-assurance.sh resolve <finding-id> "ledger proof restored"
```

## Emergency intake pause

Pause hanya menolak pembuatan topup intent/payout baru. Webhook yang telah
dibayar, payout worker, settle, cancel, replay, reconciliation, dan reversal
tetap berjalan.

```bash
scripts/product-assurance.sh pause payin <uuid> <revision> "PA01 money mismatch"
scripts/product-assurance.sh pause payout <uuid> <revision> "PO05 vendor backlog"
```

Jika assurance-service mati, principal role `admin` dapat memakai endpoint
owner direct-pause `/admin/payin/intake/pause` atau
`/admin/payout/intake/pause`. Tidak ada direct-resume.

Resume memerlukan principal kedua:

```bash
scripts/product-assurance.sh resume payout <uuid> <revision> "request resume after review"
scripts/product-assurance.sh approve payout <uuid>
```

Requester dan approver yang sama selalu ditolak. Command UUID dan revision
menjaga retry tetap idempotent; perubahan dianggap sukses hanya setelah owner
mengonfirmasi persistence.
