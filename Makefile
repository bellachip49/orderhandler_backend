COMPOSE_FILE := backend/docker-compose.yml

.PHONY: local-up local-down local-destroy local-logs local-test openapi

local-up:
	docker compose -f $(COMPOSE_FILE) up --build

local-down:
	docker compose -f $(COMPOSE_FILE) down

local-destroy:
	docker compose -f $(COMPOSE_FILE) down -v

local-logs:
	docker compose -f $(COMPOSE_FILE) logs -f

local-test:
	cd backend && go test ./...

openapi:
	@printf '%s\n' 'OpenAPI: backend/openapi.yaml'
	@printf '%s\n' 'Swagger UI: http://127.0.0.1:8088'
