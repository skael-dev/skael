package server

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"

	"github.com/skael-dev/skael/internal/platform"
	"github.com/skael-dev/skael/internal/settings"
)

// ReadyChecks holds the per-dependency readiness status.
// Each field is either "ok" or "unavailable" — real errors are logged
// server-side and never included in the HTTP response.
type ReadyChecks struct {
	Database string `json:"database"`
	Storage  string `json:"storage"`
	// MasterKey is "degraded" when a key is lost or a secret does not open.
	// The server stays ready, so an owner can reach the recovery page.
	MasterKey string `json:"master_key"`
}

// readinessChecks pings both dependencies and returns the per-check status
// ("ok" or "unavailable") plus overall readiness. Real errors are logged,
// not returned, so internal connection details are never exposed to callers.
func readinessChecks(ctx context.Context, pool *pgxpool.Pool, storage platform.Storage, keys *settings.KeyService) (ReadyChecks, bool) {
	checks := ReadyChecks{Database: "ok", Storage: "ok", MasterKey: "ok"}
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

	if keys != nil && ready {
		st, err := keys.Status(ctx)
		if err != nil {
			log.Error().Err(err).Msg("readiness: master key check failed")
			checks.MasterKey = "unknown"
		} else if st.Degraded() {
			checks.MasterKey = "degraded"
		}
	}
	return checks, ready
}
