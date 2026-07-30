set dotenv-load

# List available commands
default:
    @just --list

# --- Build ---

# Build everything (generate client + build SPA + build Go binaries)
build: generate web-build
    CGO_ENABLED=0 go build -o bin/skael-server ./cmd/server
    CGO_ENABLED=0 go build -o bin/skael ./cmd/skael
    CGO_ENABLED=0 go build -o bin/whetstone ./cmd/whetstone

# Build server only
build-server:
    CGO_ENABLED=0 go build -o bin/skael-server ./cmd/server

# Build CLI only
build-cli:
    CGO_ENABLED=0 go build -o bin/skael ./cmd/skael

# Build the whetstone eval CLI
build-whetstone:
    CGO_ENABLED=0 go build -o bin/whetstone ./cmd/whetstone

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

# Sandbox + execution tests (needs a Docker daemon). -p 1 runs the matched
# packages' test binaries one at a time rather than concurrently: Sweep()
# (internal/eval/sandbox/docker/sweep.go) filters by a docker-daemon-wide
# label with no per-process scoping, and internal/eval/sandbox/docker's own
# TestSweep_RemovesOrphanedContainersAndNetworks and
# TestSweep_LeavesUnrelatedContainersAlone zero its age guard to sweep
# instantly. Run concurrently with any other docker-tagged package, that
# reaps the other package's live, still-running containers outright — this
# reproduced twice against tests/whetstone before -p 1 was added; see
# tests/whetstone/e2e_docker_test.go's package doc and the task report for
# the full account. Serializing is the safe fix available without touching
# Sweep's design.
test-docker:
    go test -tags docker -p 1 ./internal/eval/... ./cli/whetstone/ ./tests/whetstone/... -count=1 -timeout 2400s

# Sandbox tests against the slim CI base image
test-docker-ci:
    WHETSTONE_BASE_TAG=whetstone-base-ci:1 go test -tags docker -p 1 ./internal/eval/... ./cli/whetstone/ ./tests/whetstone/... -count=1 -timeout 2400s

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
