# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

A Go backend for a food-booth order workflow. An iPad client submits food orders to this backend, the backend validates and stores the order in SQLite, assigns a readable order number, returns that confirmation to the iPad, and dispatches the order to two printers:

- a cashier printer for the customer-facing ticket
- a kitchen printer for food preparation

The repo has started the transition toward a backend-centered architecture. The current implementation is still intentionally compact under `backend/`, but it now owns order persistence, catalog/menu data, printer configuration, and print status records.

## Commands

Run from the `backend/` directory:

```sh
go run .                    # start the current prototype server
go test ./...               # run all tests
go test -run TestName ./... # run a single test
HOST=127.0.0.1 go run .     # restrict to localhost only
PORT=8081 go run .          # use a different port
DB_PATH=/tmp/orders.db go run . # use a different SQLite file
```

Docker:

```sh
docker build -t orderbackend -f Dockerfile .
docker run --rm -p 8080:8080 orderbackend
docker compose up --build
```

There is no separate lint command configured; rely on `go vet`/`go build` catching issues.

## Current state

The current implementation is a compact first refactor:

- All application code is still in `backend/main.go`.
- Tests live in `backend/main_test.go`.
- The HTTP API exposes `/health`, temporary legacy `/orders`, and versioned `/api/v1/...` routes.
- Orders, order items, sale items, printer configuration, and print jobs are persisted in SQLite using `modernc.org/sqlite`.
- Money is stored as integer cents.
- Order items are snapshotted into `order_items` at checkout.
- Sale items have active/inactive status; `/api/v1/catalog` returns only active items.
- Printer dispatch is backend-owned through a TCP adapter; missing or disabled printer config marks jobs as skipped.
- All routes except `/health` require HTTP Basic Auth.

When touching current behavior, verify claims against the code and tests.

## Target architecture

The intended refactor direction is to make the backend the system of record and the owner of operational workflows.

Expected backend responsibilities:

- continue maturing the versioned APIs for iPad/mobile clients
- validate and persist orders in SQLite
- generate a readable order number separate from the internal DB primary key
- manage catalog/menu items and prices in the backend
- manage printer configuration in the backend
- send separate cashier and kitchen tickets to two network printers
- track print status and retries independently per printer

Likely architectural split after refactoring:

- HTTP/API layer
- domain models and validation
- SQLite repositories
- printer adapter/service
- ticket rendering/templates
- background print worker
- configuration and lightweight device auth

Do not treat the current single-file structure as the desired long-term design.

## Planned API surface

Implemented endpoints:

- `GET /api/v1/catalog` for active iPad catalog retrieval
- `GET /api/v1/sale-items`, `POST /api/v1/sale-items`, `PUT /api/v1/sale-items/:id`, and `DELETE /api/v1/sale-items/:id`
- `GET /api/v1/printers`, `GET /api/v1/printers/:role`, and `PUT /api/v1/printers/:role`
- `POST /api/v1/orders` to accept an order, persist it, allocate an order number, create print jobs, and return confirmation
- `GET /api/v1/orders` and `GET /api/v1/orders/:id`
- `GET /health`

Still future work:

- filtered order-list endpoints
- printer test-print endpoint
- order reprint endpoint
- readiness endpoint
- stronger production authentication beyond Basic Auth

Behavioral expectation for the target design:

- the backend confirms an order after successful DB persistence
- printing happens asynchronously after save
- printer failure must not invalidate a saved order

## Data model expectations

The target design should move toward explicit tables/models for:

- orders
- order items
- sale items
- printers
- print jobs

Important modeling rules:

- money should be stored as integer cents, not floating-point values
- visible `order_number` should be separate from internal DB IDs
- order records should keep an immutable snapshot of sold item names/prices/quantities at checkout time
- print tracking should be per printer, not a single all-or-nothing flag

SQLite is the correct database for now, but repository boundaries should make later migration possible.

## Catalog ownership

Catalog/menu configuration should be backend-owned.

- The backend is the source of truth for items, prices, and item availability.
- The iPad should fetch catalog data from the backend.
- The iPad may later act as a UI for configuration, but it should not remain the system of record for menu data.

During transition work, call out explicitly if the mobile client is still sending ad hoc item definitions that bypass a backend-owned catalog.

## Printing workflow

Printing should be backend-owned.

- The backend should connect to the cashier and kitchen printers over the LAN.
- Cashier and kitchen should use distinct ticket templates.
- Printing should happen asynchronously after the order is committed.
- Each printer should have its own delivery status, retry behavior, and failure reporting.
- A saved order must remain queryable and reprintable even if one printer fails.

Keep printer vendor/protocol details behind an adapter boundary so the application logic does not depend on one printer implementation.

## Security and deployment assumptions

Near-term deployment assumption:

- trusted private LAN environment

Near-term security model:

- HTTP Basic Auth for mobile clients, configured by `BASIC_AUTH_USERNAME` and `BASIC_AUTH_PASSWORD`
- no full user account system yet

Configuration should remain environment-driven where practical, including:

- host and port
- database path
- timezone if needed for business logic
- printer connection settings
- device/shared-secret auth settings

## Historical notes

- `version_log.md` at the repo root tracks behavior changes through `v0.0.4`; read it before changing behavior that may have existing rationale.
- The current implementation stores timestamps in `America/Los_Angeles` and embeds tzdata via `_ "time/tzdata"`; if that changes during refactoring, do it intentionally and update documentation/tests together.
- The current implementation uses HTTP Basic Auth on all non-health routes.
