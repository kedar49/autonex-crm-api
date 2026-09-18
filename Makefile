.PHONY: help run worker seed test lint fmt build migrate-up migrate-down migrate-version docker

help:
	@grep -E '^[a-z-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  %-16s %s\n", $$1, $$2}'

run:            ## Run the HTTP gateway
	go run ./cmd/gateway

worker:         ## Run the NATS worker
	go run ./cmd/worker

seed:           ## Seed the database
	go run ./cmd/seed

test:           ## Run all tests with the race detector
	go test -race ./...

lint:           ## gofmt check + go vet
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then echo "These files need gofmt -w:"; echo "$$unformatted"; exit 1; fi
	go vet ./...

fmt:            ## Rewrite files with gofmt
	gofmt -w .

build:          ## Build the gateway binary into bin/
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/gateway ./cmd/gateway

# Migrations require golang-migrate and DATABASE_URL.
#   go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@v4.17.1
migrate-up:     ## Apply all pending migrations
	migrate -path migrations -database "$(DATABASE_URL)" up

migrate-down:   ## Roll back the most recent migration
	migrate -path migrations -database "$(DATABASE_URL)" down 1

migrate-version: ## Print the current schema version
	migrate -path migrations -database "$(DATABASE_URL)" version

docker:         ## Build the production image locally
	docker build -t autonex-crm-api:local .
