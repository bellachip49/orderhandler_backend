# OrderBackend

A local Go HTTP service for the ReceiptPrinterApp project. This initial version is independent of the iOS app and provides health and in-memory order endpoints.

## Prerequisite

Install Go 1.26.5 or a compatible newer Go release.

## Run locally

From the `backend` directory, start the server:

```sh
go run .
```

The server listens only on this Mac at `http://127.0.0.1:8080`. Verify it with:

```sh
curl http://127.0.0.1:8080/health
```

Expected response:

```json
{"status":"ok"}
```

## Endpoints

- `GET /health` returns `{"status":"ok"}`.
- `GET /orders` returns a JSON array of all orders created since the server started.
- `POST /orders` accepts an order, assigns it an incrementing numerical `id` and a `created_at` timestamp in Pacific time (`America/Los_Angeles`, hardcoded regardless of the host or container's local timezone), and returns it with HTTP 201. Orders are kept only in memory and are cleared when the server stops.
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

Stop the service with `Ctrl-C`; it will finish in-flight requests before exiting.

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

The container listens on `0.0.0.0` internally (set via `HOST` in the image) so the published port is reachable from the host; local (non-Docker) runs still default to `127.0.0.1` unless `HOST` is set explicitly.

## Run with Docker Compose

From the `backend` directory, build and start the service:

```sh
docker compose up --build
```

Verify it the same way as a local run:

```sh
curl http://127.0.0.1:8080/health
```

Stop it with `Ctrl-C`, or `docker compose down` if it was started detached (`-d`).

To use a different port, set `PORT` before starting; it is used for both the host mapping and the container's internal port:

```sh
PORT=8081 docker compose up --build
```

## Test

From the `backend` directory:

```sh
go test ./...
```
