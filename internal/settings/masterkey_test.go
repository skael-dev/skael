package settings_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skael-dev/skael/internal/platform"
	"github.com/skael-dev/skael/internal/settings"
	"github.com/skael-dev/skael/internal/testutil"
)

func TestMasterKey_AFileKeyIsGeneratedOnceAndALostKeyIsRecovered(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	dir := t.TempDir()
	env := envOf(map[string]string{"SKAEL_DATA_DIR": dir})
	snap := settings.Resolve(0, env, nil, nil)

	start := func() (*settings.MasterKey, *settings.Live, *settings.KeyService) {
		cfg := &platform.Config{}
		mk, err := settings.EnsureMasterKey(cfg, snap)
		require.NoError(t, err)
		live := settings.NewLive(settings.NewStore(pool, mk.Keys), env, mk.Keys)
		require.NoError(t, live.Load(ctx))
		ks := settings.NewKeyService(pool, mk, live)
		require.NoError(t, ks.EnsureCanary(ctx))
		return mk, live, ks
	}

	first, live, _ := start()
	require.True(t, first.Generated)
	require.Equal(t, filepath.Join(dir, settings.KeyFile), first.Path)
	info, err := os.Stat(first.Path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	require.NoError(t, live.Store().Apply(ctx, "owner@x.com", []settings.SettingsChange{{Key: "smtp.password", Value: str("hunter2")}}, env))

	again, _, ks := start()
	require.False(t, again.Generated)
	require.Equal(t, first.Keys.Fingerprint(), again.Keys.Fingerprint())
	st, err := ks.Status(ctx)
	require.NoError(t, err)
	require.False(t, st.Degraded())

	require.NoError(t, os.Remove(first.Path))
	lost, live, ks := start()
	require.True(t, lost.Generated)
	require.NotEqual(t, first.Keys.ActiveID(), lost.Keys.ActiveID())
	st, err = ks.Status(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{first.Keys.ActiveID()}, st.Missing)
	require.Equal(t, []settings.Unreadable{{Name: "SMTP password", Where: "Instance settings → Email"}}, st.Unreadable)
	require.Empty(t, live.Get().String("smtp.password"))

	require.NoError(t, ks.Reset(ctx, "owner@x.com"))
	st, err = ks.Status(ctx)
	require.NoError(t, err)
	require.False(t, st.Degraded())
	require.False(t, live.Get().Get("smtp.password").Unreadable)
}
