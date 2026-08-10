COMPOSE_FILE := backend/docker-compose.yml
BACKEND_DIR := backend
BIN_DIR := bin
APP_NAME := orderbackend
PI5_GOOS := linux
PI5_GOARCH := arm64
PI5_USER ?= kwan-pi01
PI5_HOST ?=
PI5_DEST_DIR ?= /opt/pos-web/orderbackend

.PHONY: local-up local-down local-destroy local-logs local-test openapi build build-pi5 deploy-pi5

local-up:
	docker compose -f $(COMPOSE_FILE) up --build

local-down:
	docker compose -f $(COMPOSE_FILE) down

local-destroy:
	docker compose -f $(COMPOSE_FILE) down -v

local-logs:
	docker compose -f $(COMPOSE_FILE) logs -f

local-test:
	cd $(BACKEND_DIR) && go test ./...

openapi:
	@printf '%s\n' 'OpenAPI: backend/openapi.yaml'
	@printf '%s\n' 'Swagger UI: http://127.0.0.1:8088'

build:
	cd $(BACKEND_DIR) && go build -o ../$(BIN_DIR)/$(APP_NAME) .

build-pi5:
	mkdir -p $(BIN_DIR)
	cd $(BACKEND_DIR) && GOOS=$(PI5_GOOS) GOARCH=$(PI5_GOARCH) go build -o ../$(BIN_DIR)/$(APP_NAME)-linux-arm64 .

deploy-pi5: build-pi5
	@test -n "$(PI5_HOST)" || (echo "PI5_HOST is required, for example: make deploy-pi5 PI5_HOST=192.168.1.50" && exit 1)
	@ssh $(PI5_USER)@$(PI5_HOST) "sudo systemctl stop pos-web && sudo mkdir -p $(PI5_DEST_DIR)"
	@scp $(BIN_DIR)/$(APP_NAME)-linux-arm64 $(PI5_USER)@$(PI5_HOST):/tmp/$(APP_NAME)-linux-arm64
	@ssh $(PI5_USER)@$(PI5_HOST) "sudo install -m 0755 /tmp/$(APP_NAME)-linux-arm64 $(PI5_DEST_DIR)/$(APP_NAME) && rm -f /tmp/$(APP_NAME)-linux-arm64 && sudo systemctl start pos-web"
