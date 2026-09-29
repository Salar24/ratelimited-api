.PHONY: run test test-integration lint build up down load

run: ## Run locally with in-memory store and limiter
	go run ./cmd/server

test: ## Unit tests
	go test -race ./...

test-integration: ## Unit + integration tests against the compose Redis/Postgres
	docker compose up -d --wait redis postgres
	REDIS_ADDR=localhost:6379 \
	TEST_DATABASE_URL=postgres://links:links@localhost:5432/links?sslmode=disable \
	go test -race -count=1 ./...

lint:
	golangci-lint run ./...

build:
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/server ./cmd/server

up: ## Full stack: 2 API replicas, nginx, Postgres, Redis, Prometheus, Grafana
	docker compose up -d --build

down:
	docker compose down -v

load: ## Fire 50 requests and tally status codes
	@for i in $$(seq 1 50); do curl -s -o /dev/null -w '%{http_code}\n' localhost:8080/api/v1/links/golang; done | sort | uniq -c
