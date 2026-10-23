.PHONY: help run build test test-race vet fmt fmt-check check node-check node-test assets db-up db-down db-logs docker-build db-migrate-up db-migrate-down db-migrate-status sqlc-generate integration-test

help:
	@echo "Targets: run build test test-race vet fmt fmt-check check node-check node-test assets db-up db-down db-migrate-up db-migrate-status sqlc-generate integration-test docker-build"

run:
	go run ./cmd/server

build:
	go build -o bin/budget-tracker ./cmd/server

test:
	go test ./...

test-race:
	go test -race ./...

vet:
	go vet ./...

fmt:
	go fmt ./...

fmt-check:
	@test -z "$$(gofmt -l cmd internal tests)" || (gofmt -l cmd internal tests && exit 1)

check: fmt-check vet test
	go mod verify
	go mod tidy -diff

node-check:
	npm ci --ignore-scripts
	npm run check:node

node-test:
	npm test

assets:
	npm run assets:build

db-up:
	docker compose up -d db

db-down:
	docker compose down

db-logs:
	docker compose logs -f db

db-migrate-up:
	@test -n "$$DATABASE_URL" || (echo "Export DATABASE_URL first" && exit 1)
	go run github.com/pressly/goose/v3/cmd/goose@v3.29.0 -dir db/migrations postgres "$$DATABASE_URL" up

db-migrate-down:
	@test -n "$$DATABASE_URL" || (echo "Export DATABASE_URL first" && exit 1)
	go run github.com/pressly/goose/v3/cmd/goose@v3.29.0 -dir db/migrations postgres "$$DATABASE_URL" down

db-migrate-status:
	@test -n "$$DATABASE_URL" || (echo "Export DATABASE_URL first" && exit 1)
	go run github.com/pressly/goose/v3/cmd/goose@v3.29.0 -dir db/migrations postgres "$$DATABASE_URL" status

sqlc-generate:
	go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1 generate

integration-test:
	@test -n "$$TEST_DATABASE_URL" || (echo "Export TEST_DATABASE_URL first" && exit 1)
	go test -tags=integration -count=1 ./tests/integration

docker-build:
	docker build -t golang-budget-tracker:local .
