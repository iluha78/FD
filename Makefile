.PHONY: build build-api build-ingestor build-worker run-api run-ingestor run-worker infra infra-down migrate lint test

# Build all services
build: build-api build-ingestor build-worker

build-api:
	go build -o bin/api.exe ./cmd/api
	powershell -Command "Unblock-File bin/api.exe"

build-ingestor:
	go build -o bin/ingestor.exe ./cmd/ingestor
	powershell -Command "Unblock-File bin/ingestor.exe"

build-worker:
	go build -o bin/worker.exe ./cmd/worker
	powershell -Command "Unblock-File bin/worker.exe"

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
	docker exec -i fd-postgres psql -U fd -d fd < internal/storage/migrations/002_events_embedding_index.sql
	docker exec -i fd-postgres psql -U fd -d fd < internal/storage/migrations/003_events_frame_key.sql

# Lint
lint:
	golangci-lint run ./...

# Test
test:
	go test ./... -v -race

# Download ML models
models:
	bash scripts/download_models.sh
