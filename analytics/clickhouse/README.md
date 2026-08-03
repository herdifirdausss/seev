# ClickHouse warehouse

The warehouse has isolated `raw`, `staging`, `core`, `mart`, and `control`
databases. Root migrations are applied in lexical order by the init job. Raw
CDC is append-oriented with a 30-day TTL; curated financial marts retain only
approved analytical fields for the documented local period.
