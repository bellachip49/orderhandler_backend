# Backend Printer Refactor Using CashierDesk Commit `7e21b96`

## Summary
Refactor the backend printing implementation to match the ESC/POS behavior from the latest committed CashierDesk printer code while keeping backend order numbers. The cashier printer will produce a customer receipt; the kitchen printer will produce a food-only preparation ticket. Both will use raw TCP printing, timeout handling, ESC/POS initialization, feed spacing, and full-cut commands based on the committed iOS implementation.

## Key Changes
- Replace the backend placeholder `renderTicket` with two explicit ticket renderers:
  - Cashier receipt: `Order #`, date/time, item rows, total, thank-you line, divider, feed, full cut.
  - Kitchen ticket: large/clear `Order #`, date/time, item quantities/names only, divider, feed, full cut.
- Match the CashierDesk commit's printer instructions:
  - raw TCP to configured `host:port`
  - default port `9100`
  - 8 second print timeout
  - ESC/POS initialize command `ESC @`
  - 30-character divider width
  - ASCII payload output
  - paper feed with six newline characters
  - full cut command `GS V 0`
- Update print dispatch behavior:
  - Use cashier template for `printer_role = cashier`
  - Use kitchen food-only template for `printer_role = kitchen`
  - Mark jobs `sent`, `failed`, or `skipped` exactly as today
  - Preserve saved order even if either printer fails
- Update backend tests to assert:
  - ESC/POS command bytes are present
  - cashier receipt contains order number, item line totals, total, and thank-you text
  - kitchen ticket contains order number and item quantities/names but no prices or total
  - print service writes rendered bytes to a TCP-like fake connection
  - failed connection/write timeout updates print job status correctly where covered
- Update docs/OpenAPI only if response fields or print status behavior changes; no API request/response shape changes are needed.

## Implementation Notes
- Keep implementation in `backend/main.go` for this pass, matching the current compact backend structure.
- Introduce small helpers for formatting:
  - `formatMoney(cents int) string`
  - `formatReceiptDate(createdAt string) string`
  - `divider(width int) string`
  - `cashierTicket(order Order) []byte`
  - `kitchenTicket(order Order) []byte`
- Keep the existing `PrinterService` interface but adjust `TCPPrinterService` internals so timeout/write behavior mirrors the iOS `TCPReceiptPrinter` intent in Go.
- Use `order.CreatedAt` when rendering tickets; if parsing fails, print the stored timestamp rather than blocking printing.

## Test Plan
- Run backend tests with `go test ./...`.
- Add renderer unit tests using a fixed order and timestamp.
- Add printer service tests with fake dial/write behavior so no real printer is required.
- Keep existing catalog/order/printer config tests passing.
- Validate the local stack still renders OpenAPI/Swagger with `docker compose config`.

## Assumptions
- The order number should print on both cashier and kitchen tickets.
- Cashier and kitchen printers use the same ESC/POS protocol and default port.
- Kitchen output should be food-only: no subtotal, no total, no thank-you receipt copy.
- No public API changes are required for this refactor.
