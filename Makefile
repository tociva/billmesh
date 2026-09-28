.PHONY: build run-api run-worker migrate test test-unit test-integration test-integration-clean test-e2e test-performance test-clean fmt sqlc

INTEGRATION_COMPOSE := docker compose -f deploy/compose.test.yml -f deploy/compose.dev.yml -p billmesh-integration
INTEGRATION_DATABASE_URL ?= postgres://billmesh:testpassword@127.0.0.1:5433/billmesh_integration?sslmode=disable
INTEGRATION_RESTORE_DATABASE_URL ?= postgres://billmesh:testpassword@127.0.0.1:5433/billmesh_restore?sslmode=disable
E2E_COMPOSE := docker compose -f deploy/compose.test.yml -p billmesh-test
PERFORMANCE_COMPOSE := docker compose -f deploy/compose.test.yml -p billmesh-performance

build:
	go build -o billmesh ./cmd/billmesh

run-api:
	go run ./cmd/billmesh api

run-worker:
	go run ./cmd/billmesh worker

migrate:
	go run ./cmd/billmesh migrate up

test: test-unit

test-unit:
	@TEST_SUITE_NAME='Unit tests' ./scripts/test-output.sh go test -v -race -coverprofile=coverage.out ./internal/... ./tests/unit/...

test-integration:
	@set -eu; \
	cleanup() { $(INTEGRATION_COMPOSE) down -v --remove-orphans; }; \
	trap cleanup EXIT INT TERM; \
	$(INTEGRATION_COMPOSE) down -v --remove-orphans; \
	$(INTEGRATION_COMPOSE) up -d --wait postgres; \
	DATABASE_URL='$(INTEGRATION_DATABASE_URL)' go run ./cmd/billmesh migrate up; \
	$(INTEGRATION_COMPOSE) exec -T postgres psql -U billmesh -d postgres -v ON_ERROR_STOP=1 -c 'CREATE DATABASE billmesh_restore TEMPLATE billmesh_integration'; \
	DATABASE_URL='$(INTEGRATION_DATABASE_URL)' BILLMESH_RESTORE_DATABASE_URL='$(INTEGRATION_RESTORE_DATABASE_URL)' TEST_SUITE_NAME='Integration tests' ./scripts/test-output.sh go test -p=1 -v -race -count=1 -tags=integration ./tests/integration/...

test-integration-clean:
	$(INTEGRATION_COMPOSE) down -v --remove-orphans

test-e2e:
	@set -eu; \
	cleanup() { $(E2E_COMPOSE) down -v --remove-orphans; }; \
	trap cleanup EXIT INT TERM; \
	$(E2E_COMPOSE) down -v --remove-orphans; \
	$(E2E_COMPOSE) up --build --abort-on-container-exit --exit-code-from e2e e2e

test-performance:
	@set -eu; \
	cleanup() { $(PERFORMANCE_COMPOSE) down -v --remove-orphans; }; \
	trap cleanup EXIT INT TERM; \
	$(PERFORMANCE_COMPOSE) down -v --remove-orphans; \
	$(PERFORMANCE_COMPOSE) up --build --abort-on-container-exit --exit-code-from performance performance

test-clean:
	$(E2E_COMPOSE) down -v --remove-orphans
	$(PERFORMANCE_COMPOSE) down -v --remove-orphans

fmt:
	gofmt -w $$(find . -name '*.go' -not -path './vendor/*')

sqlc:
	sqlc generate
