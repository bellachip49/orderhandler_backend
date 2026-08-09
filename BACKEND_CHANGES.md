# Backend Changes

This file records the backend refactor and API work completed for the cashier ordering workflow.

## Application Refactor

- Reworked the backend into a full order-management API instead of the original prototype order endpoint.
- Added versioned `/api/v1` routes while keeping a temporary legacy `/orders` endpoint for compatibility.
- Added persistent SQLite storage for sale items, printer configuration, orders, order line items, and print jobs.
- Added automatic migration support for the old prototype `orders` table by renaming it to a legacy table when needed.
- Added default seeded sale items and default seeded printer rows.
- Added generated order numbers beginning at `1001`.
- Stored prices and totals as integer cents to avoid floating-point money errors.

## Sale Item APIs

- Added active catalog endpoint for the ordering screen.
- Added sale-item management endpoints for settings.
- Supported creating, updating, deleting, activating, and deactivating sale items.
- Ensured inactive sale items are excluded from the order menu and rejected during order creation.

## Printer APIs

- Added printer configuration endpoints for cashier and kitchen printers.
- Stored printer host, port, display name, role, and enabled state.
- Added printer validation and persistence through the API.
- Logged disabled printers during order processing, including which printer roles were disabled.

## Order APIs

- Added order submission endpoint that accepts sale item IDs and quantities.
- Snapshotted item names and prices at the time of ordering.
- Calculated item subtotals and order totals on the backend.
- Returned persisted order details and the generated order number to the frontend.
- Added order listing and order detail endpoints.
- Created print jobs for cashier and kitchen printer roles when orders are submitted.

## Printing Refactor

- Refactored receipt rendering to match the latest frontend printer instructions.
- Added ESC/POS initialization, alignment, dividers, line formatting, feed, and cut commands.
- Rendered cashier receipts with prices, subtotals, totals, and thank-you text.
- Rendered kitchen tickets without prices or totals.
- Added TCP printer dispatch with configurable timeout and full-write validation.
- Added print-job status handling for successful, failed, skipped, disabled, and unconfigured printer cases.

## Basic Auth

- Added Basic Auth protection to all backend API routes except `/health`.
- Added environment variables for credentials:
  - `BASIC_AUTH_USERNAME`
  - `BASIC_AUTH_PASSWORD`
- Added default local-development credentials.
- Added failed-authentication logging with method, path, remote address, and attempted username.
- Avoided logging passwords.

## Local Development

- Updated Docker Compose for a local backend stack with persistent SQLite data.
- Added Swagger UI service for OpenAPI documentation.
- Added a root Makefile with targets to start, stop, destroy, inspect, and test the local stack.
- Added README curl examples for each backend function.

## OpenAPI

- Added `backend/openapi.yaml`.
- Documented Basic Auth security.
- Documented health, catalog, sale item, printer, and order endpoints.

## Tests

- Added and expanded backend tests for authentication, catalog behavior, sale item CRUD, printer configuration, order creation, legacy compatibility, printing, disabled printers, TCP printer behavior, and error cases.
- Verified the backend with `go test ./...`.
