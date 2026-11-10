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
	// Evaluation is "degraded" when no judge and panel are set, or a
	// provider they use has no readable key. The server stays ready.
	Evaluation string `json:"evaluation"`
}

// evalProblems reports why evaluation cannot run.
type evalProblems interface {
	Problems(ctx context.Context) ([]string, error)
}

// readinessChecks pings both dependencies and returns the per-check status
// ("ok" or "unavailable") plus overall readiness. Real errors are logged,
// not returned, so internal connection details are never exposed to callers.
func readinessChecks(ctx context.Context, pool *pgxpool.Pool, storage platform.Storage, keys *settings.KeyService, eval evalProblems) (ReadyChecks, bool) {
	checks := ReadyChecks{Database: "ok", Storage: "ok", MasterKey: "ok", Evaluation: "ok"}
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
	if eval != nil && ready {
		problems, err := eval.Problems(ctx)
		switch {
		case err != nil:
			log.Error().Err(err).Msg("readiness: evaluation check failed")
			checks.Evaluation = "unknown"
		case len(problems) > 0:
			checks.Evaluation = "degraded"
		}
	}
	return checks, ready
}
