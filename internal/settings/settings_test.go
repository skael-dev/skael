package settings_test

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"

	"github.com/skael-dev/skael/internal/platform"
	"github.com/skael-dev/skael/internal/settings"
	"github.com/skael-dev/skael/internal/testutil"
)

func envOf(m map[string]string) settings.EnvFunc {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func str(s string) *string { return &s }

func TestResolve_EnvWinsAndAnInvalidEnvValueKeepsTheDefault(t *testing.T) {
	stored := map[string]settings.Stored{
		"smtp.host":       {Value: "ui.example.com"},
		"rate_limit.read": {Value: "50"},
	}
	snap := settings.Resolve(1, envOf(map[string]string{"SMTP_HOST": "env.example.com", "RATE_LIMIT_READ": "lots"}), stored, nil)

	host := snap.Get("smtp.host")
	require.Equal(t, "env.example.com", host.Value)
	require.Equal(t, settings.SourceEnv, host.Source)
	require.Equal(t, "SMTP_HOST", settings.Field(host).LockedBy)

	read := snap.Get("rate_limit.read")
	require.Equal(t, 300, snap.Int("rate_limit.read"))
	require.Contains(t, read.Problem, "RATE_LIMIT_READ=lots is invalid")
}

func TestLoadConfig_RefusesAnInvalidQualityFloor(t *testing.T) {
	_, _, err := settings.LoadConfig(envOf(map[string]string{"DATABASE_URL": "postgres://x", "QUALITY_FLOOR": "150"}))
	require.ErrorContains(t, err, "QUALITY_FLOOR=150 is invalid")

	cfg, _, err := settings.LoadConfig(envOf(map[string]string{"DATABASE_URL": "postgres://x"}))
	require.NoError(t, err)
	require.Equal(t, ":8080", cfg.ListenAddr)
	require.True(t, cfg.MetricsEnabled)
}

func TestCheck_RefusesEnvOnlyLockedAndInvalidChanges(t *testing.T) {
	env := envOf(map[string]string{"SMTP_HOST": "env.example.com"})
	cases := map[string]settings.SettingsChange{
		"is set only through":     {Key: "listen_addr", Value: str(":9090")},
		"wins over the UI":        {Key: "smtp.host", Value: str("ui.example.com")},
		"must be at most 100":     {Key: "quality_floor", Value: str("101")},
		"takes no reference":      {Key: "smtp.port", Reference: str("env:PORT")},
		"a reference is env:NAME": {Key: "smtp.password", Reference: str("vault:x")},
		"unknown setting":         {Key: "nope", Value: str("1")},
	}
	for want, c := range cases {
		require.ErrorContains(t, settings.Check([]settings.SettingsChange{c}, env), want)
	}
	require.NoError(t, settings.Check([]settings.SettingsChange{{Key: "smtp.port", Value: str("465")}}, env))
}

func testKeys(t *testing.T) *platform.Keyring {
	t.Helper()
	k, err := platform.ParseKeyring("k1:" + base64.StdEncoding.EncodeToString(make([]byte, 32)))
	require.NoError(t, err)
	return k
}

func TestStore_SealsSecretsResolvesReferencesAndReloadsOnNotify(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	keys := testKeys(t)
	secretFile := filepath.Join(t.TempDir(), "token")
	require.NoError(t, os.WriteFile(secretFile, []byte("ghp_file\n"), 0o600))
	env := envOf(map[string]string{"OIDC_SECRET_SOURCE": "from-env"})

	writer := settings.NewLive(settings.NewStore(pool, keys), env, keys)
	reader := settings.NewLive(settings.NewStore(pool, keys), env, keys)
	require.NoError(t, reader.Load(ctx))
	watchCtx, stop := context.WithCancel(ctx)
	defer stop()
	go reader.Watch(watchCtx)
	time.Sleep(200 * time.Millisecond)

	require.NoError(t, writer.Store().Apply(ctx, "owner@x.com", []settings.SettingsChange{
		{Key: "smtp.password", Value: str("hunter2")},
		{Key: "github_token", Reference: str("file:" + secretFile)},
		{Key: "oidc.client_secret", Reference: str("env:OIDC_SECRET_SOURCE")},
		{Key: "rate_limit.write", Value: str("120")},
	}, env))

	require.Eventually(t, func() bool { return reader.Get().Int("rate_limit.write") == 120 }, 5*time.Second, 50*time.Millisecond)
	snap := reader.Get()
	require.Equal(t, "hunter2", snap.String("smtp.password"))
	require.Equal(t, "ghp_file", snap.String("github_token"))
	require.Equal(t, "from-env", snap.String("oidc.client_secret"))

	var sealed string
	require.NoError(t, pool.QueryRow(ctx, `SELECT sealed FROM settings WHERE key = 'smtp.password'`).Scan(&sealed))
	require.NotContains(t, sealed, "hunter2")

	body := settings.Body(snap)
	for _, sec := range body.Sections {
		for _, f := range sec.Fields {
			if f.Key == "smtp.password" {
				require.Empty(t, f.Value)
				require.True(t, f.IsSet)
			}
		}
	}

	var audited int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action = 'settings.update'`).Scan(&audited))
	require.Equal(t, 4, audited)

	require.NoError(t, writer.Store().Apply(ctx, "owner@x.com", []settings.SettingsChange{{Key: "rate_limit.write", Clear: true}}, env))
	require.Eventually(t, func() bool { return reader.Get().Int("rate_limit.write") == 60 }, 5*time.Second, 50*time.Millisecond)
}

func TestMigration032AddsTheSettingsTables(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	pool := testutil.SetupTestDBAtVersion(t, 31)
	db := stdlib.OpenDBFromPool(pool)
	defer db.Close() //nolint:errcheck
	require.NoError(t, platform.MigrateUpTo(db, 32))
	var version int64
	require.NoError(t, pool.QueryRow(context.Background(), `SELECT version FROM settings_version`).Scan(&version))
	require.Zero(t, version)
}
