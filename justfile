set dotenv-load

# List available commands
default:
    @just --list

# --- Build ---

# Build everything (generate client + build SPA + build Go binaries)
# The version is stamped from git rather than left at main.version's "dev"
# default: the server rejects a report from a worker reporting "dev", so an
# unstamped worker runs a whole tier and is refused at the last step. Using
# `git describe` rather than a constant also keeps report.Comparable able to
# tell two local builds apart.
build: generate web-build
    #!/usr/bin/env bash
    set -euo pipefail
    v=$(git describe --tags --always --dirty 2>/dev/null || echo 0.0.0-local)
    ldflags="-X main.version=${v}"
    CGO_ENABLED=0 go build -ldflags "$ldflags" -o bin/skael-server ./cmd/server
    CGO_ENABLED=0 go build -ldflags "$ldflags" -o bin/skael ./cmd/skael
    CGO_ENABLED=0 go build -ldflags "$ldflags" -o bin/whetstone ./cmd/whetstone
    CGO_ENABLED=0 go build -ldflags "$ldflags" -o bin/skael-worker ./cmd/skael-worker

# Build server only
build-server:
    CGO_ENABLED=0 go build -o bin/skael-server ./cmd/server

# Build CLI only
build-cli:
    CGO_ENABLED=0 go build -o bin/skael ./cmd/skael

# Build the whetstone eval CLI
build-whetstone:
    CGO_ENABLED=0 go build -o bin/whetstone ./cmd/whetstone

# Build the eval queue worker
build-worker:
    CGO_ENABLED=0 go build -o bin/skael-worker ./cmd/skael-worker

# --- Web / Frontend ---

# Generate OpenAPI spec + TypeScript client
generate:
    go run ./cmd/server --openapi > web/openapi.json
    cd web && npm run generate

# Run Vite dev server
web-dev:
    cd web && npm run dev

# Build the SPA
web-build:
    cd web && npm run build

# Run Astro dev server for the landing page
site-dev:
    cd site && npm run dev

# Build the landing page
site-build:
    cd site && npm run build

# --- Dev ---

# Run the server locally (requires DATABASE_URL and API_KEY in .env or environment)
dev:
    go run ./cmd/server

# Start Postgres in Docker for local development
db:
    docker run --rm -d --name skael-dev-db \
        -e POSTGRES_USER=skael -e POSTGRES_PASSWORD=skael -e POSTGRES_DB=skael \
        -p 5432:5432 postgres:17
    @echo "Postgres running on localhost:5432"
    @echo "DATABASE_URL=postgres://skael:skael@localhost:5432/skael?sslmode=disable"

# Stop the local dev database
db-stop:
    docker stop skael-dev-db

# Start the full stack via Docker Compose
up:
    docker compose up -d

# Stop the full stack
down:
    docker compose down

# Stop and remove volumes (fresh start)
down-clean:
    docker compose down -v

# --- Test ---

# Run all tests except e2e (needs Docker for Go testcontainers)
test:
    go test ./... -count=1
    cd web && npx vitest run

# Run tests with verbose output
test-v:
    go test ./... -v -count=1

# Run tests for a specific package
test-pkg pkg:
    go test ./{{pkg}} -v -count=1

# Run a single test by name
test-run name:
    go test ./... -v -count=1 -run {{name}}

# Full Playwright E2E (starts server, needs Docker)
test-e2e:
    cd web && npx playwright test

# Go integration e2e suite (real server + Postgres testcontainers)
test-integration:
    go test -tags integration ./tests/e2e/ -count=1

# Frontend unit/integration tests (fast, no server needed)
test-web:
    cd web && npx vitest run

# Fast feedback loop (<10s) — skip testcontainers + e2e
test-fast:
    go test -short ./... -count=1 && cd web && npx vitest run

# Test the eval engine only
test-eval:
    go test ./internal/eval/... ./cli/whetstone/... -count=1

# Sandbox + execution tests (needs a Docker daemon). Safe to run at default
# parallelism against any other docker-tagged package: every whetstone
# container and network is labeled with its creating process's pid
# (internal/eval/sandbox/docker/labels.go), and Sweep only removes a resource
# once that pid is confirmed dead (see sweep.go's pidAlive) — a still-running
# process's containers are never touched, regardless of test-induced age
# zeroing elsewhere on the daemon.
test-docker:
    go test -tags docker ./internal/eval/... ./cli/whetstone/ ./tests/whetstone/... -count=1 -timeout 2400s

# Sandbox tests against the slim CI base image
test-docker-ci:
    WHETSTONE_BASE_TAG=whetstone-base-ci:1 go test -tags docker ./internal/eval/... ./cli/whetstone/ ./tests/whetstone/... -count=1 -timeout 2400s

# --- Lint / Check ---

# Run go vet
vet:
    go vet ./...

# Format all Go files
fmt:
    gofmt -w .

# Check formatting (CI-friendly, exits non-zero if unformatted)
fmt-check:
    @test -z "$(gofmt -l .)" || (echo "unformatted files:" && gofmt -l . && exit 1)

# Tidy go.mod
tidy:
    go mod tidy

# Run all checks (vet + fmt + test + integration)
check: vet fmt-check test test-integration

# --- Docker ---

# Build the Docker image
docker-build:
    docker compose build

# View server logs
logs:
    docker compose logs -f server

# --- Migrations ---

# Run pending migrations
migrate:
    go run ./cmd/migrate up

# Rollback the last migration
migrate-down:
    go run ./cmd/migrate down

# Show migration status
migrate-status:
    go run ./cmd/migrate status

# Create a new migration file
migrate-create name:
    @mkdir -p internal/platform/migrate
    goose -dir internal/platform/migrate create {{name}} sql
    @echo "Created migration in internal/platform/migrate/"

# --- Scan ---

# Run security scan on a skill directory
scan dir:
    go run ./cmd/skael scan {{dir}}

# Lint a skill bundle with the eval engine's linter
lint-skill path:
    go run ./cmd/whetstone lint {{path}}
