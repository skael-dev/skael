package providers_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skael-dev/skael/internal/evalqueue"
	"github.com/skael-dev/skael/internal/platform"
	"github.com/skael-dev/skael/internal/providers"
	"github.com/skael-dev/skael/internal/testutil"
)

func keyring(t *testing.T) *platform.Keyring {
	t.Helper()
	pair, err := platform.GenerateKey()
	require.NoError(t, err)
	k, err := platform.ParseKeyring(pair)
	require.NoError(t, err)
	return k
}

// One key in Settings is enough to run: the first preset fills the empty
// panel, the claim carries the key, and the database holds it sealed.
func TestPresets_OneKeyFillsThePanelAndTheClaimCarriesIt(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	s := providers.NewStore(pool, keyring(t), nil)

	problems, err := s.Problems(ctx)
	require.NoError(t, err)
	require.Len(t, problems, 1, "an instance with no panel must say so")

	_, err = s.ApplyPreset(ctx, "openrouter", "", "", "sk-or-secret", "owner@x.com")
	require.NoError(t, err)
	cfg, err := s.EvalConfig(ctx)
	require.NoError(t, err)
	require.Equal(t, "openrouter", cfg.Judge.Type)
	require.Equal(t, "sk-or-secret", cfg.Judge.Credential)
	require.Len(t, cfg.Members, 2)
	require.Equal(t, "sk-or-secret", cfg.Members[0].Credential)

	var sealed string
	require.NoError(t, pool.QueryRow(ctx, `SELECT credential_sealed FROM providers WHERE name = 'OpenRouter'`).Scan(&sealed))
	require.NotEmpty(t, sealed)
	require.False(t, strings.Contains(sealed, "sk-or-secret"), "the key must be stored sealed")

	list, err := s.List(ctx)
	require.NoError(t, err)
	require.True(t, list[0].HasKey)

	problems, err = s.Problems(ctx)
	require.NoError(t, err)
	require.Empty(t, problems)

	// A second provider never replaces a panel the owner already has.
	before, err := s.Panel(ctx)
	require.NoError(t, err)
	_, err = s.ApplyPreset(ctx, "anthropic", "", "", "sk-ant", "owner@x.com")
	require.NoError(t, err)
	after, err := s.Panel(ctx)
	require.NoError(t, err)
	require.Equal(t, before.Judge, after.Judge)
	require.Equal(t, before.Members, after.Members)

	require.ErrorContains(t, s.Delete(ctx, before.Judge.ProviderID, "owner@x.com"), "uses this provider")
}

// A reference is read when a job is claimed; a missing variable is reported
// as a provider that needs a key.
func TestProvider_AReferenceIsReadAtClaim(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	s := providers.NewStore(pool, keyring(t), nil)

	t.Setenv("SKAEL_TEST_PROVIDER_KEY", "from-env")
	_, err := s.ApplyPreset(ctx, "anthropic", "", "", "env:SKAEL_TEST_PROVIDER_KEY", "owner@x.com")
	require.NoError(t, err)
	cfg, err := s.EvalConfig(ctx)
	require.NoError(t, err)
	require.Equal(t, "from-env", cfg.Judge.Credential)
	list, err := s.List(ctx)
	require.NoError(t, err)
	require.Equal(t, "env:SKAEL_TEST_PROVIDER_KEY", list[0].KeyRef)

	t.Setenv("SKAEL_TEST_PROVIDER_KEY", "")
	problems, err := s.Problems(ctx)
	require.NoError(t, err)
	require.Len(t, problems, 1)
	require.Contains(t, problems[0], "Anthropic needs a key")
}

func TestProvider_ValidateRefusesWhatCannotWork(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	s := providers.NewStore(pool, keyring(t), nil)

	for _, c := range []struct {
		p    providers.Provider
		want string
	}{
		{providers.Provider{Name: "A", Type: "anthropic", BaseURL: "https://x.example/v1"}, "leave /v1 off"},
		{providers.Provider{Name: "B", Type: "openai-compatible"}, "needs the base URL"},
		{providers.Provider{Name: "C", Type: "openrouter", AuthHeader: "api-key"}, "only an OpenAI-compatible"},
		{providers.Provider{Name: "D", Type: "gateway"}, "the type must be one of"},
	} {
		p := c.p
		require.ErrorContains(t, s.Save(ctx, &p, "k", "owner@x.com"), c.want, c.p.Name)
	}
	_, err := s.ApplyPreset(ctx, "azure", "", "", "k", "owner@x.com")
	require.ErrorContains(t, err, "needs its base URL")

	azure, err := s.ApplyPreset(ctx, "azure", "https://r.openai.azure.com/openai/v1", "api-key", "k", "owner@x.com")
	require.NoError(t, err)
	require.Equal(t, "api-key", azure.AuthHeader)
	panel, err := s.Panel(ctx)
	require.NoError(t, err)
	require.Nil(t, panel, "a preset with no default models leaves the panel to the owner")
}

// A key sealed under a lost master key is listed for re-entry, and the reset
// drops it.
func TestProvider_AKeyUnderALostMasterKeyIsReported(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	_, err := providers.NewStore(pool, keyring(t), nil).ApplyPreset(ctx, "openai", "", "", "sk-1", "owner@x.com")
	require.NoError(t, err)

	s := providers.NewStore(pool, keyring(t), nil)
	bad, err := s.Unreadable(ctx)
	require.NoError(t, err)
	require.Len(t, bad, 1)
	require.Contains(t, bad[0].Name, "OpenAI")

	require.NoError(t, s.Discard(ctx, "owner@x.com"))
	bad, err = s.Unreadable(ctx)
	require.NoError(t, err)
	require.Empty(t, bad)
	list, err := s.List(ctx)
	require.NoError(t, err)
	require.False(t, list[0].HasKey)
}

// A run on a model outside the panel names its provider; the claim then gives
// that member the named provider's key, and the judge stays the configured one.
func TestEvalConfigFor_ARequestedModelRunsOnItsProvider(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	s := providers.NewStore(pool, keyring(t), nil)
	_, err := s.ApplyPreset(ctx, "anthropic", "", "", "sk-ant", "owner@x.com")
	require.NoError(t, err)
	or, err := s.ApplyPreset(ctx, "openrouter", "", "", "sk-or", "owner@x.com")
	require.NoError(t, err)

	cfg, err := s.EvalConfigFor(ctx, evalqueue.Panel{Models: []string{"deepseek/deepseek-v4.1-flash"}, Providers: []string{or.ID}})
	require.NoError(t, err)
	require.Equal(t, "anthropic", cfg.Judge.Type)
	require.Len(t, cfg.Members, 1)
	require.Equal(t, "sk-or", cfg.Members[0].Credential)
	require.Equal(t, "deepseek/deepseek-v4.1-flash", cfg.Members[0].Model)

	cfg, err = s.EvalConfigFor(ctx, evalqueue.Panel{Models: []string{"claude-haiku-5-5"}})
	require.NoError(t, err)
	require.Equal(t, "sk-ant", cfg.Members[0].Credential, "a model with no provider runs on the primary member's")
}
