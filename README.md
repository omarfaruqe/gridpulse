# GridPulse

[Français](README.fr.md)

A data pipeline for Canadian electricity. It collects hourly demand, generation and price data from Hydro-Québec, IESO (Ontario) and AESO (Alberta), joins it with Environment and Climate Change Canada weather, checks it against Statistics Canada, and serves a tested warehouse through a JSON API, a bilingual dashboard and a pipeline-health page. Written in Go.

What it shows:

- **Ingestion** that can be repeated safely: raw files go to object storage and are never modified.
- **A warehouse with tests**: 14 SQL data quality tests run after every transform, and the results are kept and shown on the health page.
- **Reconciliation** of Ontario generation against Statistics Canada, with documented findings, including the ones that are not explained yet (see [docs/data-notes.md](docs/data-notes.md)).
- **Operations**: a scheduler with locks, timeouts and recorded runs; freshness limits per series; CI with integration tests; Docker Compose.

[Architecture and diagram](docs/architecture.md) · [Data notes and known gaps](docs/data-notes.md)

## Quick start

Requirements: Docker with Compose. Go (see `go.mod`) only if you want to build or test outside containers.

```sh
cp .env.example .env                      # optional: add a free AESO API key for Alberta
docker compose --profile app up -d --build
```

Then open:

- http://localhost:8080 for the dashboard (English or French, `EN`/`FR` link in the header)
- http://localhost:8080/pipeline for freshness, job runs and test results
- http://localhost:8080/api/v1/health for the API

The scheduler starts collecting at once. Series fill in over the first hour; Ontario demand updates only about once a day, because of how IESO publishes it. To backfill history, see the extractor examples below.

## Working on the code

```sh
docker compose up -d                      # only Postgres (:5433) and SeaweedFS (S3 on :8333)
go test ./...                             # unit tests
go test -tags integration ./...           # also needs the two services above
go build -o bin/loader ./cmd/loader && ./bin/loader -migrate
go run ./cmd/scheduler -list              # jobs and their schedules
go run ./cmd/scheduler -once transform    # run one job now
go run ./cmd/api -addr :8080
```

Backfilling history with the extractor:

```sh
go build -o bin/extractor ./cmd/extractor
./bin/extractor -source hydroquebec
./bin/extractor -source ieso -report Demand -from-year 2025 -to-year 2026
./bin/extractor -source ieso -report GenOutputbyFuelHourly -from-year 2022 -to-year 2026
./bin/extractor -source aeso -report PoolPrice -from 2026-11-01 -to 2026-11-07
./bin/extractor -source eccc -from 2026-11-05 -to 2026-11-06
./bin/extractor -source statcan
./bin/loader -source all                  # load everything in bronze into Postgres
```

Settings come from environment variables whose defaults match `docker-compose.yml`: `GRIDPULSE_S3_ENDPOINT`, `GRIDPULSE_S3_BUCKET`, `GRIDPULSE_S3_ACCESS_KEY`, `GRIDPULSE_S3_SECRET_KEY`, `GRIDPULSE_S3_USE_SSL`, `GRIDPULSE_POSTGRES_DSN`, `GRIDPULSE_AESO_API_KEY`. A git-ignored `.env` is loaded automatically. AESO needs a free key from developer-apim.aeso.ca; without one the Alberta jobs are not scheduled.

## Data sources

| Source | Series |
|---|---|
| Hydro-Québec | Real-time demand (15-minute slots, aggregated to hours) |
| IESO | Generator output by fuel, hourly demand, day-ahead zonal price |
| AESO | Pool price, Alberta internal load |
| ECCC | Hourly weather from six stations mapped to the three regions |
| Statistics Canada | Monthly generation by type, table 25-10-0015-01 (used for reconciliation) |

Not included yet: IESO real-time 5-minute prices, refined station weights, Canada Energy Regulator context data. Hydro-Québec open data is CC BY-NC 4.0; see [docs/data-notes.md](docs/data-notes.md) for the licence note.

## Licence

The code is MIT licensed (see `LICENSE`). The data belongs to its providers and keeps their terms; Hydro-Québec's is CC BY-NC 4.0.

## Limits

This is a portfolio project. It is not an operational or trading tool: the API has no authentication, the freshness limits are tuned to publication cadence rather than to a service level, and several reconciliation differences are measured but not explained. They are listed, with their status, in [docs/data-notes.md](docs/data-notes.md).
