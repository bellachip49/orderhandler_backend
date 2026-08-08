# OrderBackend

A local Go HTTP service for the ReceiptPrinterApp project. This initial version is independent of the iOS app and provides health and order endpoints, with orders persisted to a local SQLite database.

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

The first time this happens, macOS may show a firewall prompt ("Do you want the incoming network connections to be allowed?") — allow it. Note that the server has no authentication, so anyone on the same network can reach these endpoints while it's running. To restrict it back to this Mac only, set `HOST=127.0.0.1`:

```sh
HOST=127.0.0.1 go run .
```

## Endpoints

- `GET /health` returns `{"status":"ok"}`.
- `GET /orders` returns a JSON array of all orders created since the server started.
- `POST /orders` accepts an order, assigns it an incrementing numerical `id` and a `created_at` timestamp in Pacific time (`America/Los_Angeles`, hardcoded regardless of the host or container's local timezone), and returns it with HTTP 201. Orders are persisted to a local SQLite database, so they survive server restarts.
- Other methods on `/orders` return HTTP 405 with `Allow: GET, POST`.

Create an order using the menu items from ReceiptPrinterApp:

```sh
curl --request POST http://127.0.0.1:8080/orders \
  --header 'Content-Type: application/json' \
  --data '{
    "items": [
      {"name": "Milk Tea", "price": 4.50, "quantity": 2},
      {"name": "Dumplings", "price": 6.00, "quantity": 1}
    ],
    "total": 15.00
  }'
```

Each order must have at least one item; each item needs a name, non-negative price, and quantity of at least one. The total must not be negative.

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

Verify it the same way as a local run:

```sh
curl http://127.0.0.1:8080/health
```

Stop it with `Ctrl-C`, or `docker compose down` if it was started detached (`-d`). The SQLite database is stored in the `orders-data` named volume (mounted at `/data`), so orders persist across `docker compose down`/`up` as long as the volume isn't removed (`docker compose down -v` deletes it).

To use a different port, set `PORT` before starting; it is used for both the host mapping and the container's internal port:

```sh
PORT=8081 docker compose up --build
```

## Test

From the `backend` directory:

```sh
go test ./...
```
