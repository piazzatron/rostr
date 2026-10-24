# Checks for the Go modules. `make check` runs what CI runs, except the end-to-end tests (`make e2e`).
# The linters are pinned and run with `go run`, so there's nothing to install first. They're built with the module's
# own Go version, since golangci-lint won't lint code newer than the Go it was built with.

MODULES := cli server tests
GOLANGCI_LINT := GOTOOLCHAIN=$$(go env GOVERSION) go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0
GOVULNCHECK := GOTOOLCHAIN=$$(go env GOVERSION) go run golang.org/x/vuln/cmd/govulncheck@v1.9.0
TEST_DATABASE_URL ?= postgres://rostr:rostr@localhost:5432/rostr?sslmode=disable

.PHONY: check lint fmt vuln test test-cli test-server postgres e2e

## check: lint and unit tests for every module
check: lint test

## lint: golangci-lint (vet, staticcheck, errcheck, gofmt, goimports, ...) over every module; see .golangci.yml
lint:
	@for m in $(MODULES); do echo "lint $$m"; (cd $$m && $(GOLANGCI_LINT) run ./...) || exit 1; done

## fmt: rewrite files with gofmt and goimports
fmt:
	@for m in $(MODULES); do (cd $$m && $(GOLANGCI_LINT) fmt ./...) || exit 1; done

## vuln: known vulnerabilities in the code the CLI and server call
vuln:
	@for m in cli server; do echo "vuln $$m"; (cd $$m && $(GOVULNCHECK) ./...) || exit 1; done

## test: unit tests for the CLI and server (starts the compose Postgres for the server's)
test: test-cli test-server

test-cli:
	cd cli && go test -race ./...

test-server: postgres
	cd server && TEST_DATABASE_URL='$(TEST_DATABASE_URL)' go test -race ./...

## postgres: start the compose Postgres and wait for it
postgres:
	@docker compose up -d --wait postgres

## e2e: the end-to-end tests against the full Docker Compose stack
e2e:
	tests/run.sh
