# Kafka Connect/Debezium

The image is pinned to Debezium Connect `3.1.2.Final` and builds the small
`PseudonymizeField` SMT from source. Connector JSON is declarative and uses
placeholders for source credentials; `apply-connectors.sh` substitutes those
values in memory and never prints the request body.

`snapshot.mode=initial`, `pgoutput`, explicit publications, stable slots,
heartbeats, source LSN metadata, delete rewrite, one-partition topics, and
`slot.drop.on.stop=false` are deliberate C2 learning defaults.
