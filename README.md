# GridPulse

A pipeline that ingests hourly electricity data from Canadian grid operators, joins it with Environment and Climate Change Canada weather observations, and serves a tested analytical warehouse through an API and dashboard. Written in Go.

**Status:** extractors for Hydro-Québec, IESO and AESO are done and write idempotent, backfillable raw files to bronze. Next: ECCC weather, then Postgres loading with silver and gold models.

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

# IESO (Ontario): daily reports, or the yearly Demand file
./bin/extractor -source ieso -report GenOutputCapability -from 2026-11-01 -to 2026-11-07
./bin/extractor -source ieso -report Demand -from-year 2025 -to-year 2026
./bin/extractor -source ieso -report DAHourlyOntarioZonalPrice -date 2026-11-06

# AESO (Alberta): needs a free API key in .env
./bin/extractor -source aeso -report PoolPrice -from 2026-11-01 -to 2026-11-07
./bin/extractor -source aeso -report AlbertaInternalLoad -date 2026-11-06
```

Optional integration test against the running object store:

```sh
go test -tags integration ./internal/bronze/
```

Settings come from environment variables (defaults match `docker-compose.yml`): `GRIDPULSE_S3_ENDPOINT`, `GRIDPULSE_S3_BUCKET`, `GRIDPULSE_S3_ACCESS_KEY`, `GRIDPULSE_S3_SECRET_KEY`, `GRIDPULSE_S3_USE_SSL`, `GRIDPULSE_POSTGRES_DSN`, `GRIDPULSE_AESO_API_KEY`. A git-ignored `.env` in the project root is loaded automatically (see `.env.example`). AESO needs a free API key from developer-apim.aeso.ca.

## Data sources

| Source | Status |
|---|---|
| Hydro-Québec real-time demand (15-minute slots, about 48 hours) | Done |
| IESO: generator output by fuel, hourly demand, day-ahead zonal price | Done |
| IESO: real-time 5-minute zonal price (hourly files) | Planned |
| AESO: pool price, Alberta internal load | Done |
| ECCC weather observations | Sprint 3 |
| Statistics Canada reconciliation | Sprint 6 |

Hydro-Québec open data is published under CC BY-NC 4.0. It is raw data without a quality guarantee, and timestamps in the feed are Eastern local time without an offset.

## Time handling

Every source uses a different clock, so conversion to UTC is explicit and tested (`internal/timeutil`):

| Source | Clock | Handling |
|---|---|---|
| Hydro-Québec | Eastern local time, no offset | The repeated hour when clocks fall back is flagged `ambiguous`; the skipped hour when they spring forward is flagged `nonexistent` |
| IESO | Fixed EST (UTC-5) all year, hour-ending | Hour N starts at N-1 o'clock EST; no daylight saving shifts |
| AESO | UTC (`begin_datetime_utc`) | The local `begin_datetime_mpt` field is ignored: Alberta moved to year-round UTC-6 ("Alberta Time") on 2026-11-01, so history spans two time rule sets and UTC avoids the issue |

Empty values in source files are kept as nulls, never zero.
