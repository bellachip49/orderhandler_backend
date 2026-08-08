# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

A local Go HTTP backend for the ReceiptPrinterApp iOS project. It is currently independent of the iOS app — a standalone service exposing health and order endpoints, backed by SQLite. All code lives under `backend/`.

## Commands

Run from the `backend/` directory:

```sh
go run .                 # start the server (binds 0.0.0.0:8080 by default)
go test ./...             # run all tests
go test -run TestName ./... # run a single test
HOST=127.0.0.1 go run .  # restrict to localhost only
PORT=8081 go run .       # use a different port
DB_PATH=/tmp/orders.db go run .  # use a different SQLite file (defaults to orders.db in the working dir)
```

Docker:

```sh
docker build -t orderbackend -f Dockerfile .
docker run --rm -p 8080:8080 orderbackend
docker compose up --build   # via docker-compose.yml
```

There is no separate lint command configured; rely on `go vet`/`go build` catching issues.

## Architecture

Everything (routing, handlers, storage, model, validation) is in a single file, `backend/main.go`, deliberately kept small and flat for this early-stage service. Tests live in `backend/main_test.go`, using `httptest` against the handler returned by `newHandler`.

Key points to know before making changes:

- **Storage**: `orderStore` wraps a `*sql.DB` (SQLite via `modernc.org/sqlite`, a pure-Go driver — no CGO needed, which is why the Dockerfile builds with `CGO_ENABLED=0`). The `orders` table stores `items` as a JSON-encoded text column; `orderStore.add`/`orderStore.all` handle the marshal/unmarshal. Tests use `openDatabase(":memory:")` for isolation.
- **Timestamps**: `created_at` is always written in `America/Los_Angeles` regardless of host/container timezone (see `pacificLocation` in main.go). The blank `_ "time/tzdata"` import embeds the IANA tz database in the binary so this resolves correctly on minimal images (e.g. Alpine) that lack `/usr/share/zoneinfo`. Don't switch this to local time or remove the tzdata import without accounting for that.
- **Routing**: no router library — `newHandler` is a single `http.HandlerFunc` doing manual path/method dispatch. `/health` (GET only) and `/orders` (GET/POST; other methods get 405 with an `Allow` header) are the only routes; everything else 404s as JSON.
- **Request handling**: `createOrderHandler` enforces a strict single-JSON-object body (rejects trailing content via a second `Decode` call checking for `io.EOF`) and unknown fields (`DisallowUnknownFields`), with a 1MB body cap via `MaxBytesReader`. `Order.validate()` holds all business validation (non-empty items, non-negative price/total, quantity ≥ 1).
- **Host/port/db-path config**: all three are read from environment variables (`HOST`, `PORT`, `DB_PATH`) with defaults `0.0.0.0`, `8080`, `orders.db` — see `hostFromEnvironment`/`portFromEnvironment`/`dbPathFromEnvironment`.
- **Shutdown**: `main` selects between a server-error channel and an OS signal channel (SIGINT/SIGTERM) to perform graceful shutdown with a 5s timeout.

## Notable behavior/history

- No authentication on any endpoint — anyone reachable on the network can call it while it's running.
- `version_log.md` at the repo root tracks behavioral changes release by release (v0.0.1 → v0.0.3); check it for the rationale behind past changes (e.g. why `HOST` defaults to `0.0.0.0`, why timestamps are hardcoded to Pacific time) before altering that behavior.
