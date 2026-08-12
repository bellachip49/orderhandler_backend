# Version Log

## v1.1.0

Added backend support for the frontend Order History window and improved observability for that flow.

- Updated `GET /api/v1/orders` with an optional `limit` query parameter so the iPad can request the most recent orders without loading the full history.
- Changed order-history listing to return newest limited results first and to load order headers before item and print-job detail queries, avoiding SQLite single-connection stalls during history reads.
- Added backend server logging for successful order-history responses, including the applied limit, returned count, and compact JSON payload sent to the frontend.
- Updated `backend/openapi.yaml` to document the order-history limit parameter.
- Added backend tests for limited newest-first history, invalid limits, logged order-history payloads, and empty history logging.

## v1.1

Extended the backend API to own sales-summary rendering and printing while keeping the frontend as a thin client for summary retrieval and print requests.

- Added `GET /api/v1/order-summary` to report the current sales summary, including per-item quantity and revenue breakdowns derived from historical `order_items` snapshots.
- Added `POST /api/v1/order-summary/print` so the backend can print the cashier sales summary itself, using the configured cashier printer and returning success or failure to the caller.
- Updated the summary aggregation to count only currently active sale items, so disabled items are excluded from total earned, order count, and printed sales summary output.
- Added backend receipt rendering for sales summaries with title, timestamp, order count, total earned, and item breakdown lines, reusing the backend printer encoding support.
- Extended printer dispatch so the backend printer service can print both order tickets and sales-summary tickets over the same TCP path and error handling.
- Updated the OpenAPI document and backend tests to cover the new summary print endpoint, filtered summary behavior, printer validation, and printer failure responses.

## v1

Completed the backend API refactor for the cashier ordering workflow, making the backend the system of record for catalog, orders, printer configuration, persistence, authentication, API documentation, and receipt printing.

- Added versioned `/api/v1` APIs for catalog retrieval, sale-item management, printer configuration, order submission, order listing, and order detail lookup while keeping the temporary legacy `/orders` endpoint for older clients.
- Added backend-owned sale item storage with `name`, `description`, `price_cents`, `active`, timestamps, default seed data, and active-only catalog behavior for the ordering screen.
- Added sale item create, update, and inactive/delete behavior for the settings workflow; inactive items stay in the settings list, are hidden from `/api/v1/catalog`, and are rejected during order creation.
- Added backend-owned order creation from sale item IDs and quantities, including generated readable order numbers, integer-cent totals, duplicate item validation, inactive/missing item validation, and saved item snapshots.
- Added `description` snapshots to `order_items` so printed and historical orders keep the sale item description from checkout time even if the catalog item changes later.
- Added SQLite tables and migrations for sale items, printer configuration, orders, order items, and print jobs, including compatibility handling for the original prototype `orders` table.
- Added cashier and kitchen printer configuration APIs with role validation, host/port/enabled persistence, default printer rows, and default TCP port `9100`.
- Added per-printer print jobs for each submitted order, with status updates for sent, failed, skipped, disabled, and unconfigured printer cases.
- Refactored printing to backend-owned raw TCP dispatch with timeout handling, full-write validation, ESC/POS initialization, alignment, dividers, feed spacing, and full-cut commands.
- Added separate receipt templates: cashier receipts include item subtotals, totals, and thank-you text; kitchen tickets are food-only and omit prices, totals, and thank-you text.
- Added receipt item descriptions under item names for both cashier and kitchen tickets.
- Added Chinese receipt text support with configurable `PRINTER_TEXT_ENCODING`; UTF-8 is the default for the physical printer, while `gbk` remains available as a compatibility fallback with SDK Chinese-mode commands.
- Added receipt text sanitizing that removes emojis and emoji joiners/variation selectors before sending printable text to printers.
- Added HTTP Basic Auth to all routes except `/health`, configurable with `BASIC_AUTH_USERNAME` and `BASIC_AUTH_PASSWORD`, with failed-authentication logging that avoids logging passwords.
- Added `backend/openapi.yaml` and Swagger UI support through the local Docker Compose stack.
- Updated Docker Compose for local testing with persistent SQLite storage, API service, and Swagger UI, and added a root Makefile for starting, stopping, destroying, inspecting, and testing the local stack.
- Expanded README documentation with setup, configuration, Basic Auth, printer encoding, local stack usage, Swagger UI access, and curl examples for backend functions.
- Expanded backend tests for authentication, catalog behavior, sale item CRUD and inactive behavior, printer configuration, order creation, order snapshots, OpenAPI coverage, TCP printer behavior, disabled printer logging, receipt rendering, Chinese text encoding, emoji omission, and error cases.

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
