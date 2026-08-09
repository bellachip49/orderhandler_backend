# Backend Refactor for iPad Ordering + Settings

## Summary
Refactor the backend into the system of record for menu, printer configuration, and order processing, so the iPad app can run two windows: one for settings management and one for order entry. The backend will own active/inactive sale items, persist submitted orders, generate a readable order number, and handle post-save dispatch to cashier and kitchen printers.

## Key Changes
- Split backend data modeling from the current single `orders` JSON blob into explicit backend-owned entities:
  - sale items with `active` status
  - printer configuration records
  - orders with immutable line-item snapshots
  - print job records per printer
- Replace the current `/orders`-only API with a versioned API that supports:
  - catalog retrieval for the ordering window
  - CRUD for sale items in the settings window
  - printer configuration save/load
  - order submission that returns an order number after persistence
  - order lookup/reprint endpoints for operational recovery
- Make order submission flow:
  - validate submitted items against backend catalog state
  - save the order and assign a readable order number
  - enqueue cashier and kitchen print jobs independently
  - return confirmation to the iPad immediately after the save succeeds
- Update the iPad app to use backend data instead of hardcoded menu items:
  - settings window edits printer IP/config and sale items
  - order window lists only active items from the backend
  - totals/subtotals are derived from backend catalog prices
  - submit resets the order UI after confirmation returns
- Keep printer-specific implementation behind a backend adapter boundary so the app and domain logic do not depend on printer protocol details.

## Test Plan
- Backend API tests:
  - create/list/update/disable sale items
  - load/save printer configuration
  - submit order with active items only
  - reject unknown or inactive items
  - return order number on success
  - preserve order when one printer job fails
- Persistence tests:
  - sale items survive restart
  - order snapshots preserve name/price/quantity even if catalog changes later
  - print jobs track status independently per printer
- iPad integration tests:
  - settings screen loads/saves printer config
  - order screen loads active catalog items only
  - quantity, subtotal, and total calculations update correctly
  - successful submission resets the order screen and shows the returned order number
- Regression coverage:
  - existing health/error behavior remains stable where still applicable
  - current prototype endpoints are either preserved temporarily or explicitly replaced with compatibility-aware redirects

## Assumptions
- The iPad app remains the cashier-facing client and there is no separate web admin app in scope.
- LAN-only deployment remains the near-term model, with lightweight auth deferred unless you want it added now.
- The backend is authoritative for sale item names, prices, and active/inactive status.
- The settings window is allowed to manage both printer configuration and sale item CRUD directly from the iPad.
- Printing should happen after order persistence, and printer failure must not block saving the order.
