set dotenv-load

# List available commands
default:
    @just --list

# --- Build ---

# Build both binaries
build:
    CGO_ENABLED=0 go build -o bin/skael-server ./cmd/server
    CGO_ENABLED=0 go build -o bin/skael ./cmd/skael

# Build server only
build-server:
    CGO_ENABLED=0 go build -o bin/skael-server ./cmd/server

# Build CLI only
build-cli:
    CGO_ENABLED=0 go build -o bin/skael ./cmd/skael

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

# Run all tests (needs Docker for testcontainers)
test:
    go test ./... -count=1

# Run tests with verbose output
test-v:
    go test ./... -v -count=1

# Run tests for a specific package
test-pkg pkg:
    go test ./{{pkg}} -v -count=1

# Run a single test by name
test-run name:
    go test ./... -v -count=1 -run {{name}}

# Run end-to-end tests (requires integration build tag)
test-e2e:
    go test -tags integration ./tests/e2e/ -v -count=1 -timeout 120s

# Run fast tests only (no testcontainers — platform, auth, scan, CLI packages)
test-fast:
    go test ./internal/platform/ ./internal/auth/ ./internal/scan/ ./cli/... -v -count=1

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

# Run all checks (vet + fmt + test)
check: vet fmt-check test

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
