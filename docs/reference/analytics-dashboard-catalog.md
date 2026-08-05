# C2 dashboard catalog

Dashboard manifests live in [analytics/metabase/dashboards](../../analytics/metabase/dashboards/)
and are imported into a running Metabase instance via
[analytics/metabase/setup/import_dashboards.py](../../analytics/metabase/setup/import_dashboards.py).

Runtime-verified 2026-08-05: all 6 dashboards imported, all 23 cards
executed successfully against real business-e2e data through the
`bi_readonly` ClickHouse role (`metabase_bi` user), and the governance
constraints below were confirmed by direct negative test (raw/staging reads
and writes denied — see [c2-final-acceptance.md](../evidence/c2-final-acceptance.md)).

| Dashboard | Audience | Approved sources | Required warning |
| --- | --- | --- | --- |
| Executive overview | business | curated marts + control summaries | revenue/volume/cost semantics and freshness |
| Pay-in performance | Payin owner | Payin lifecycle fact | lifecycle metrics are not revenue |
| Payout performance | Payout owner | Payout lifecycle/attempt facts | destination data is not available |
| Fee and quote conversion | Ledger owner | quote and revenue marts | quote does not prove recognition |
| Modeled unit economics | finance learning | unit economics mart | modeled, not invoiced actuals or net profit |
| Data platform health | operators | freshness/control summaries | analytical health is separate from business health |

Ordinary BI users receive `bi_readonly`, which can query approved marts and
limited dimensions/control summaries only. No dashboard target is an OLTP,
raw CDC, or restricted staging table.
