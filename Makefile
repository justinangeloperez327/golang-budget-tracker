.PHONY: help run build test test-race vet fmt fmt-check check node-check node-test assets db-up db-down db-logs docker-build

help:
	@echo "Targets: run build test test-race vet fmt fmt-check check node-check node-test assets db-up db-down db-logs docker-build"

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
	@test -z "$$(gofmt -l cmd internal)" || (gofmt -l cmd internal && exit 1)

check: fmt-check vet test

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

docker-build:
	docker build -t golang-budget-tracker:local .
