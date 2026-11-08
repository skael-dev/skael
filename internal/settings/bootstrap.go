package settings

import (
	"errors"
	"os"

	"github.com/rs/zerolog/log"

	"github.com/skael-dev/skael/internal/platform"
)

// DefaultStoragePath is where a filesystem store lives when STORAGE_PATH is
// unset and the install started on the filesystem.
const DefaultStoragePath = "./data/skills"

// LoadConfig reads the env-only settings the server needs before its database.
// A StrictEnv setting with an invalid value is a start error.
func LoadConfig(env EnvFunc) (*platform.Config, *Snapshot, error) {
	if env == nil {
		env = os.LookupEnv
	}
	snap := Resolve(0, env, nil, nil)
	for _, r := range snap.Problems() {
		if r.Def.StrictEnv {
			return nil, nil, errors.New(r.Problem)
		}
		log.Warn().Str("setting", r.Def.Env).Msg(r.Problem)
	}
	if snap.String("database_url") == "" {
		return nil, nil, errors.New("DATABASE_URL is required")
	}
	keys, err := platform.ParseKeyring(snap.String("master_keys"))
	if err != nil {
		return nil, nil, err
	}
	return &platform.Config{
		DatabaseURL:         snap.String("database_url"),
		StoragePath:         snap.String("storage_path"),
		ListenAddr:          snap.String("listen_addr"),
		DBMaxConns:          snap.Int("db.max_conns"),
		DBMinConns:          snap.Int("db.min_conns"),
		DBMaxConnLifetime:   snap.Duration("db.max_conn_lifetime"),
		DBMaxConnIdleTime:   snap.Duration("db.max_conn_idle_time"),
		DBHealthCheckPeriod: snap.Duration("db.health_check_period"),
		CORSOrigins:         snap.String("cors_origins"),
		TrustedProxies:      snap.String("trusted_proxies"),
		CookieSecure:        snap.Bool("cookie_secure"),
		MetricsEnabled:      snap.Bool("metrics.enabled"),
		MasterKeys:          keys,
	}, snap, nil
}
