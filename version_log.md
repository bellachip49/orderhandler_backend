# Version Log

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
