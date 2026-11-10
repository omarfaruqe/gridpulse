# Deploying GridPulse behind a reverse proxy

This describes the setup used for the live demo: a shared Linux server with Docker, a Caddy gateway on port 80 and a path prefix (`/gridpulse/`) for the site.

## What runs

Four containers in a Compose project named `gridpulse`: Postgres, SeaweedFS (object store), the scheduler and the API. `docker-compose.server.yml` publishes only the API, on `127.0.0.1:8020`, so Postgres and the object store are not reachable from outside. Each container has a CPU and memory cap, and all restart after a reboot.

## First start

```sh
git clone https://github.com/omarfaruqe/gridpulse && cd gridpulse
cp .env.example .env
# edit .env: uncomment COMPOSE_FILE, COMPOSE_PROFILES, POSTGRES_PASSWORD (openssl rand -hex 24)
# and, if you have one, set GRIDPULSE_AESO_API_KEY
docker compose up -d --build
docker compose ps
scripts/backfill.sh        # long: hours, depending on the network
```

The scheduler starts collecting immediately; the backfill adds history.

## Reverse proxy

The API strips nothing itself: the proxy removes the prefix, and `GRIDPULSE_BASE_PATH=/gridpulse` (set in the override) makes the pages emit prefixed links.

```
redir /gridpulse /gridpulse/ permanent
handle_path /gridpulse/* {
	reverse_proxy 127.0.0.1:8020
}
```

## Known limits

- HTTP only until a domain and certificate exist.
- The API is read-only but has no authentication or rate limiting. Range queries are capped at 92 days with a 20 second timeout.
- Back up `pgdata/` and `.env`; the object store can be rebuilt from the sources only while IESO still holds the files.
