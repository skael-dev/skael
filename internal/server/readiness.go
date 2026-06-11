package server

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"

	"github.com/skael-dev/skael/internal/platform"
)

// ReadyChecks holds the per-dependency readiness status.
// Each field is either "ok" or "unavailable" — real errors are logged
// server-side and never included in the HTTP response.
type ReadyChecks struct {
	Database string `json:"database"`
	Storage  string `json:"storage"`
}

// readinessChecks pings both dependencies and returns the per-check status
// ("ok" or "unavailable") plus overall readiness. Real errors are logged,
// not returned, so internal connection details are never exposed to callers.
func readinessChecks(ctx context.Context, pool *pgxpool.Pool, storage platform.Storage) (ReadyChecks, bool) {
	checks := ReadyChecks{Database: "ok", Storage: "ok"}
	ready := true

	if err := pool.Ping(ctx); err != nil {
		log.Error().Err(err).Msg("readiness: database check failed")
		checks.Database = "unavailable"
		ready = false
	}

	if err := storage.Ping(ctx); err != nil {
		log.Error().Err(err).Msg("readiness: storage check failed")
		checks.Storage = "unavailable"
		ready = false
	}

	return checks, ready
}
