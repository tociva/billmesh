.PHONY: build run-api run-worker migrate test test-unit test-e2e test-clean fmt sqlc

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
	go test -race -coverprofile=coverage.out ./internal/...

test-e2e:
	docker compose -f deploy/compose.test.yml -p billmesh-test up --build --exit-code-from e2e e2e

test-clean:
	docker compose -f deploy/compose.test.yml -p billmesh-test down -v --remove-orphans

fmt:
	gofmt -w $$(find . -name '*.go' -not -path './vendor/*')

sqlc:
	sqlc generate
