# GridPulse

A pipeline that ingests hourly electricity data from Canadian grid operators, joins it with Environment and Climate Change Canada weather observations, and serves a tested analytical warehouse through an API and dashboard. Written in Go.

**Status:** Sprint 1 complete (repo, local stack, bronze storage, Hydro-Québec extractor).

## Architecture

| Layer | Implementation |
|---|---|
| Ingestion | One Go extractor per source, writing raw files to bronze |
| Bronze storage | S3-compatible object store (SeaweedFS locally), partitioned `source=<name>/date=<YYYY-MM-DD>/` |
| Warehouse | Postgres 17 (silver and gold SQL models, coming in later sprints) |
| Orchestration | Go scheduler with retries and freshness checks (Sprint 5) |
| Serving | Go API and server-rendered dashboard (Sprint 7) |

Design rules: extractors are idempotent (same upstream version gives the same object key), bronze is stored raw and never modified, and timestamps are normalized to UTC downstream.

## Quick start

Requirements: Go (see `go.mod`), Docker with Compose.

```sh
docker compose up -d                      # SeaweedFS (S3 on :8333) and Postgres (:5432)
go test ./...                             # unit tests
go build -o bin/extractor ./cmd/extractor
./bin/extractor -source hydroquebec       # fetch the live feed into bronze
./bin/extractor -list source=hydroquebec_demand/
```

Optional integration test against the running object store:

```sh
go test -tags integration ./internal/bronze/
```

Settings come from environment variables (defaults match `docker-compose.yml`): `GRIDPULSE_S3_ENDPOINT`, `GRIDPULSE_S3_BUCKET`, `GRIDPULSE_S3_ACCESS_KEY`, `GRIDPULSE_S3_SECRET_KEY`, `GRIDPULSE_S3_USE_SSL`, `GRIDPULSE_POSTGRES_DSN`.

## Data sources

| Source | Status |
|---|---|
| Hydro-Québec real-time demand (15-minute slots, about 48 hours) | Done |
| IESO, AESO | Sprint 2 |
| ECCC weather observations | Sprint 3 |
| Statistics Canada reconciliation | Sprint 6 |

Hydro-Québec open data is published under CC BY-NC 4.0. It is raw data without a quality guarantee, and timestamps in the feed are Eastern local time without an offset.
