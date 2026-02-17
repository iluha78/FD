.PHONY: build build-api build-ingestor build-worker run-api run-ingestor run-worker infra infra-down migrate lint test

# Build all services
build: build-api build-ingestor build-worker

build-api:
	go build -o bin/api.exe ./cmd/api

build-ingestor:
	go build -o bin/ingestor.exe ./cmd/ingestor

build-worker:
	go build -o bin/worker.exe ./cmd/worker

# Run services locally
run-api:
	go run ./cmd/api

run-ingestor:
	go run ./cmd/ingestor

run-worker:
	go run ./cmd/worker

# Infrastructure
infra:
	docker-compose -f deploy/docker-compose.yml up -d

infra-down:
	docker-compose -f deploy/docker-compose.yml down

# Database migration
migrate:
	@echo "Applying migrations..."
	docker exec -i fd-postgres psql -U fd -d fd < internal/storage/migrations/001_init.sql

# Lint
lint:
	golangci-lint run ./...

# Test
test:
	go test ./... -v -race

# Download ML models
models:
	bash scripts/download_models.sh
