# Print Chinese Receipt Text And Item Descriptions

## Summary
Add backend receipt printing support for Chinese characters using configurable printer text encoding. Default to UTF-8 because the real printer output showed GBK bytes as gibberish and the iOS SDK demo sends POS text with `NSUTF8StringEncoding`. Keep GBK as an opt-in fallback, strip emojis before printing, and print each item description directly below the item name line while leaving the rest of the cashier and kitchen receipt text unchanged.

## Key Changes
- Use SDK-aligned printer commands:
  - Keep existing `ESC @`, alignment, feed, and cut commands.
  - Default printable receipt text to UTF-8.
  - Add `PRINTER_TEXT_ENCODING=utf8|gbk`, defaulting to `utf8`.
  - For `gbk`, add `FS &` before printable text, add `FS .` before the final cut, and encode printable receipt text with GBK via `golang.org/x/text/encoding/simplifiedchinese`.
- Add safe receipt text sanitizing:
  - Preserve English, Chinese characters, numbers, punctuation, and normal whitespace.
  - Remove emoji characters and emoji joiners/variation selectors before encoding.
  - Replace characters that cannot be encoded in GBK with `?` or omit them consistently in tests.
- Snapshot item descriptions into orders:
  - Add `description` to backend `OrderItem`.
  - Add `description TEXT NOT NULL DEFAULT ''` to `order_items` with an idempotent migration for existing databases.
  - Copy `sale_items.description` into `order_items.description` when creating orders.
  - Include `description` in order list/detail API responses and OpenAPI schema.
- Update receipt template:
  - Cashier receipt keeps the existing item name/price/subtotal line.
  - If an item has a description, print it on the next row, indented under the name.
  - Kitchen receipt also prints the description under the item name, while still omitting prices, total, and thank-you text.

## Sample Receipt Preview
```text
Order #1001
------------------------------
08/09/26  2:35 PM

2  Milk Tea             $11.00
   港式奶茶
1  Dumplings             $6.00
   豬肉白菜餃子
1  Spicy Noodles         $8.00
   招牌辣麵

------------------------------
TOTAL                   $25.00

Thank you!
------------------------------
```

If a description contains emoji, for example `招牌辣麵 🔥`, the printed row becomes `招牌辣麵`.

## Tests
- Backend unit tests:
  - `cashierTicket` includes UTF-8 Chinese description bytes by default.
  - `kitchenTicket` includes Chinese descriptions but no prices, total, or thank-you text.
  - GBK fallback includes GBK-encoded Chinese description bytes and the SDK Chinese-mode commands.
  - Emoji text is omitted from printable payloads in both UTF-8 and GBK modes.
  - Existing ESC/POS prefix, alignment, feed, and cut command assertions still pass.
  - Invalid `PRINTER_TEXT_ENCODING` is rejected.
  - Order creation snapshots `description` from the active sale item.
  - Existing databases gain `order_items.description` without losing old orders.
- API/OpenAPI validation:
  - `OrderItem` response schema includes `description`.
  - OpenAPI YAML parses successfully.
- Full validation:
  - Run `go test ./...`.
  - Run the existing OpenAPI parse check.
  - Optionally submit a curl order with Chinese item descriptions and verify the generated print payload/test output contains encoded Chinese text and no emoji.

## Assumptions
- UTF-8 is the correct default for the physical printer because GBK printed as gibberish and the iOS SDK demo uses `NSUTF8StringEncoding`.
- GBK remains available because the Android SDK documentation says printer text helpers default to Chinese encoding `"gbk"`.
- The backend will implement the SDK command behavior directly in Go rather than importing iOS/Android SDK binaries.
- Empty descriptions do not print an extra blank description row.
- No frontend API changes are required because sale item descriptions already exist and are submitted to the backend.
