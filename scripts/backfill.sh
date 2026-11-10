#!/usr/bin/env bash
# Backfill history on a fresh deployment, one step at a time.
# Run from the repository root with the stack already up:  scripts/backfill.sh
# Every step is idempotent: bronze files are named by content, so running it
# twice stores nothing twice. Steps run sequentially to keep the load low on a
# shared machine; the containers are CPU-capped by docker-compose.server.yml.
set -euo pipefail

x() { docker compose run --rm --no-deps scheduler /app/extractor "$@"; }
today=$(date -u +%F)
ago()  { date -u -d "$1 days ago" +%F; }
year=$(date -u +%Y)

echo "== Hydro-Québec";            x -source hydroquebec
echo "== IESO demand";             x -source ieso -report Demand -from-year 2022 -to-year "$year"
echo "== IESO generation by fuel"; x -timeout 20m -source ieso -report GenOutputbyFuelHourly -from-year 2022 -to-year "$year"
echo "== IESO capability";         x -timeout 20m -source ieso -report GenOutputCapability -from "$(ago 60)" -to "$today"
echo "== IESO day-ahead prices";   x -timeout 20m -source ieso -report DAHourlyOntarioZonalPrice -from "$(ago 60)" -to "$today"
echo "== IESO real-time prices (about 90 days are kept)"
x -timeout 40m -source ieso -report RealtimeOntarioZonalPrice -from "$(ago 89)" -to "$today"
echo "== ECCC weather";            x -timeout 20m -source eccc -from "$(ago 30)" -to "$(ago 1)"
echo "== StatCan";                 x -source statcan
if [ -n "${GRIDPULSE_AESO_API_KEY:-}" ] || grep -q '^GRIDPULSE_AESO_API_KEY=.\+' .env 2>/dev/null; then
  echo "== AESO"
  x -source aeso -report PoolPrice -from "$(ago 30)" -to "$today"
  x -source aeso -report AlbertaInternalLoad -from "$(ago 30)" -to "$today"
else
  echo "== AESO skipped (no key in .env)"
fi

echo "== Load bronze into the warehouse"
docker compose run --rm --no-deps scheduler /app/loader -source all
echo "== Transform"
docker compose run --rm --no-deps scheduler /app/transform
echo "Backfill finished."
