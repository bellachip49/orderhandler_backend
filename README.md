# OrderBackend

A local Go HTTP service for the ReceiptPrinterApp project. The backend owns sale items, printer configuration, order persistence, order-number generation, and per-printer print job tracking.

## Prerequisite

Install Go 1.26.5 or a compatible newer Go release.

## Run locally

From the `backend` directory, start the server:

```sh
go run .
```

The server listens on all network interfaces at port `8080` by default, so it's reachable both from this Mac and from other devices on the same Wi-Fi network. Verify it locally with:

```sh
curl http://127.0.0.1:8080/health
```

Expected response:

```json
{"status":"ok"}
```

To reach it from another device on the same Wi-Fi network (e.g. an iPad running ReceiptPrinterApp), find this Mac's LAN IP address:

```sh
ipconfig getifaddr en0
```

Then from the other device:

```sh
curl http://<mac-lan-ip>:8080/health
```

The first time this happens, macOS may show a firewall prompt ("Do you want the incoming network connections to be allowed?") - allow it. API endpoints require HTTP Basic Auth. To restrict the server back to this Mac only, set `HOST=127.0.0.1`:

```sh
HOST=127.0.0.1 go run .
```

## Endpoints

- `GET /health` returns `{"status":"ok"}`.
- `GET /api/v1/catalog` returns only active sale items for the iPad ordering screen.
- `GET /api/v1/sale-items` lists all sale items, including inactive items.
- `POST /api/v1/sale-items` creates a sale item using `name`, `description`, `price_cents`, and `active`.
- `PUT /api/v1/sale-items/{id}` updates a sale item.
- `DELETE /api/v1/sale-items/{id}` marks a sale item inactive.
- `GET /api/v1/printers` lists cashier and kitchen printer configuration.
- `GET /api/v1/printers/{cashier|kitchen}` loads one printer configuration.
- `PUT /api/v1/printers/{cashier|kitchen}` saves `host`, `port`, and `enabled`.
- `GET /api/v1/orders` lists saved orders with item snapshots and print jobs.
- `GET /api/v1/orders/{id}` loads one saved order.
- `POST /api/v1/orders` accepts sale item IDs and quantities, saves the order, assigns a readable `order_number`, creates cashier and kitchen print jobs, and returns the saved order with HTTP 201.
- Legacy `GET /orders` and `POST /orders` remain temporarily available for older clients.

All endpoints except `/health` require HTTP Basic Auth. Configure credentials with `BASIC_AUTH_USERNAME` and `BASIC_AUTH_PASSWORD`; defaults are `admin` and `orderbackend`.

Receipt printer text defaults to UTF-8 so Chinese descriptions print correctly on printers that match the iOS SDK demo's `NSUTF8StringEncoding` path. If a printer is configured for GBK/Chinese mode instead, start the backend with `PRINTER_TEXT_ENCODING=gbk`.

Create an order using the menu items from ReceiptPrinterApp:

```sh
curl --user admin:orderbackend \
  --request POST http://127.0.0.1:8080/api/v1/orders \
  --header 'Content-Type: application/json' \
  --data '{
    "items": [
      {"sale_item_id": 4, "quantity": 2},
      {"sale_item_id": 1, "quantity": 1}
    ]
  }'
```

Each order must have at least one active catalog item, and each item needs a quantity of at least one. Order item names and prices are snapshotted from the backend catalog at checkout time.

To use a different port, set `PORT` when starting the service:

```sh
PORT=8081 go run .
```

Orders are stored in a SQLite database file, `orders.db` in the working directory by default. To use a different location, set `DB_PATH`:

```sh
DB_PATH=/path/to/orders.db go run .
```

Stop the service with `Ctrl-C`; it will finish in-flight requests before exiting. Restarting the service reloads existing orders from the database file.

## Run with Docker

From the `backend` directory, build and run the image:

```sh
docker build -t orderbackend -f Dockerfile .
docker run --rm -p 8080:8080 orderbackend
```

Verify it the same way as a local run:

```sh
curl http://127.0.0.1:8080/health
```

To use a different port, override `PORT` and map the matching container port:

```sh
docker run --rm -e PORT=8081 -p 8081:8081 orderbackend
```

The container listens on `0.0.0.0` internally (set via `HOST` in the image) so the published port is reachable from the host, matching the local (non-Docker) default.

The SQLite database lives inside the container's filesystem by default, so it is lost when the container is removed. To persist it, mount a volume and point `DB_PATH` at it:

```sh
docker run --rm -p 8080:8080 -e DB_PATH=/data/orders.db -v orders-data:/data orderbackend
```

## Run with Docker Compose

From the `backend` directory, build and start the service:

```sh
docker compose up --build
```

From the repo root, you can use Make instead:

```sh
make local-up
```

This starts the backend and stores SQLite at `/data/orders.db` inside the `orders-data` Docker volume. The default host port is `8080`.
It also starts Swagger UI at `http://127.0.0.1:8088`, backed by `backend/openapi.yaml`.
The Compose stack defaults to `admin` / `orderbackend` for Basic Auth. Override those with `BASIC_AUTH_USERNAME` and `BASIC_AUTH_PASSWORD`.

Verify it the same way as a local run:

```sh
curl http://127.0.0.1:8080/health
```

Stop it with `Ctrl-C`, or `docker compose down` if it was started detached (`-d`). The SQLite database is stored in the `orders-data` named volume (mounted at `/data`), so orders persist across `docker compose down`/`up` as long as the volume isn't removed (`docker compose down -v` deletes it).

From the repo root:

```sh
make local-down
```

To use a different host port for the Compose stack, set `APP_PORT` before starting:

```sh
APP_PORT=8081 docker compose up --build
```

To use a different Swagger UI host port, set `SWAGGER_PORT`:

```sh
SWAGGER_PORT=8090 docker compose up --build
```

From the repo root, print the OpenAPI file path and Swagger UI URL:

```sh
make openapi
```

Reset the local test database by deleting the named volume:

```sh
docker compose down -v
```

From the repo root:

```sh
make local-destroy
```

## Curl local test examples

Set a base URL once:

```sh
BASE_URL=http://127.0.0.1:8080
AUTH=admin:orderbackend
```

Health check:

```sh
curl "$BASE_URL/health"
```

List active catalog items for the ordering screen:

```sh
curl --user "$AUTH" "$BASE_URL/api/v1/catalog"
```

List all sale items, including inactive items:

```sh
curl --user "$AUTH" "$BASE_URL/api/v1/sale-items"
```

Create a sale item:

```sh
curl --user "$AUTH" \
  --request POST "$BASE_URL/api/v1/sale-items" \
  --header 'Content-Type: application/json' \
  --data '{"name":"Egg Tart","description":"Warm custard tart","price_cents":350,"active":true}'
```

Update a sale item, replacing `6` with the item ID returned by the create call:

```sh
curl --user "$AUTH" \
  --request PUT "$BASE_URL/api/v1/sale-items/6" \
  --header 'Content-Type: application/json' \
  --data '{"name":"Egg Tart","description":"Updated tart description","price_cents":400,"active":true}'
```

Mark a sale item inactive:

```sh
curl --user "$AUTH" --request DELETE "$BASE_URL/api/v1/sale-items/6"
```

List printer configuration:

```sh
curl --user "$AUTH" "$BASE_URL/api/v1/printers"
```

Load one printer configuration:

```sh
curl --user "$AUTH" "$BASE_URL/api/v1/printers/cashier"
```

Save cashier printer configuration:

```sh
curl --user "$AUTH" \
  --request PUT "$BASE_URL/api/v1/printers/cashier" \
  --header 'Content-Type: application/json' \
  --data '{"host":"192.168.1.50","port":9100,"enabled":true}'
```

Save kitchen printer configuration:

```sh
curl --user "$AUTH" \
  --request PUT "$BASE_URL/api/v1/printers/kitchen" \
  --header 'Content-Type: application/json' \
  --data '{"host":"192.168.1.51","port":9100,"enabled":true}'
```

Submit an order using active sale item IDs:

```sh
curl --user "$AUTH" \
  --request POST "$BASE_URL/api/v1/orders" \
  --header 'Content-Type: application/json' \
  --data '{
    "items": [
      {"sale_item_id":1,"quantity":2},
      {"sale_item_id":4,"quantity":1}
    ]
  }'
```

List saved orders:

```sh
curl --user "$AUTH" "$BASE_URL/api/v1/orders"
```

Load one order, replacing `1` with the order ID:

```sh
curl --user "$AUTH" "$BASE_URL/api/v1/orders/1"
```

Temporary legacy order endpoint for older clients:

```sh
curl --user "$AUTH" \
  --request POST "$BASE_URL/orders" \
  --header 'Content-Type: application/json' \
  --data '{"items":[{"name":"Milk Tea","price":4.5,"quantity":2}],"total":9}'
```

## Test

From the `backend` directory:

```sh
go test ./...
```
