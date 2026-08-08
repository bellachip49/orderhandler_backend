# Version Log

## v0.0.4

Persisted orders to a local SQLite database instead of keeping them only in memory.

- Orders are now stored in a SQLite database file (`orders.db` by default, configurable via the new `DB_PATH` environment variable) using the pure-Go `modernc.org/sqlite` driver, so they survive server and container restarts.
- `backend/docker-compose.yml` mounts a named volume (`orders-data`) at `/data` and sets `DB_PATH=/data/orders.db` so the database persists across `docker compose down`/`up`; `backend/Dockerfile` creates that directory with the correct ownership for the non-root container user.
- `GET /orders` and `POST /orders` now return HTTP 500 with a generic error message on unexpected database errors, matching the existing JSON error shape used for other error responses.
- No changes to request/response JSON shapes: `GET /health`, `GET /orders`, and `POST /orders` behave identically from a client's perspective, aside from orders no longer being lost on restart.
- Updated `README.md` to document `DB_PATH` and volume-mounting for Docker/Docker Compose.

## v0.0.3

Made the local server reachable from other devices on the same Wi-Fi network.

- Changed the default `HOST` from `127.0.0.1` to `0.0.0.0`, so `go run .` now binds to all network interfaces by default, matching the behavior Docker and Docker Compose already had.
- Documented how to find the Mac's LAN IP and call the API from another device, the macOS firewall prompt this can trigger, and how to restrict back to `127.0.0.1` via `HOST` if needed.
- No changes to endpoint behavior: `GET /health`, `GET /orders`, and `POST /orders` work identically to before.

## v0.0.2

Added Docker support for the backend service.

- Added a multi-stage `Dockerfile` (Go build stage, minimal Alpine runtime stage) and `.dockerignore` under `backend/`.
- Added a configurable `HOST` environment variable (mirrors the existing `PORT` override) so the server can bind to `0.0.0.0` inside a container; local runs still default to `127.0.0.1`.
- Added `backend/docker-compose.yml` for building and running the service with `docker compose`.
- `POST /orders` now hardcodes `created_at` to Pacific time (`America/Los_Angeles`) instead of the process's local timezone, so timestamps stay consistent across containers and hosts (previously containers defaulted to UTC).
- No changes to endpoint behavior otherwise: `GET /health`, `GET /orders`, and `POST /orders` work identically in and out of Docker.
- Documented Docker and Docker Compose build/run instructions in the README.

## v0.0.1

Initial local Go backend service for ReceiptPrinterApp.

- Added an HTTP server that listens on `127.0.0.1:8080` by default, with an optional `PORT` override and graceful shutdown.
- Added `GET /health` for server health checks.
- Added in-memory order APIs: `GET /orders` lists orders and `POST /orders` creates them.
- New orders receive an incrementing numeric `id` and a local-time `created_at` timestamp.
- Added JSON request validation for empty orders, blank item names, negative prices or totals, and quantities below one.
- Unsupported methods on `/orders` return HTTP 405 with the allowed methods listed.
- Added automated HTTP endpoint tests and local run instructions.
