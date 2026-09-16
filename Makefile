DB_PATH ?= data/congress.db

.PHONY: setup generate migrate test run build tidy ingest-full dev seed

setup: ## install pinned codegen/migration tools
	go install github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1
	go install github.com/pressly/goose/v3/cmd/goose@v3.28.0

generate: ## sqlc codegen (validates SQL at build time)
	sqlc generate

migrate: ## goose up against $(DB_PATH)
	goose -dir db/migrations sqlite3 $(DB_PATH) up

test:
	go test ./...

build:
	go build ./...

tidy:
	go mod tidy

ingest-full: ## one-shot historical load (resumable; single-writer)
	go run ./cmd/sync -full

seed: ## parties registry + deputies + validated affiliations
	go run ./cmd/sync -seed-parties data/parties.json -seed data/affiliations.json

dev: ## web server on :8080 with background incremental sync
	go run ./cmd/server

run: dev
