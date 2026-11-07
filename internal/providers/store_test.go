package providers_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skael-dev/skael/internal/providers"
	"github.com/skael-dev/skael/internal/testutil"
)

func TestPresets_SetUpAWorkingPanelAndRefuseASubscriptionJudge(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	s := providers.NewStore(pool)

	for _, p := range providers.Presets {
		require.NoError(t, s.ApplyPreset(ctx, p.ID, "", "owner@x.com"), p.ID)
		cfg, err := s.EvalConfig(ctx)
		require.NoError(t, err)
		require.NotEmpty(t, cfg.Members, p.ID)
		require.NotEqual(t, "subscription", cfg.Judge.Adapter, p.ID)
		if p.Subscription {
			require.Equal(t, "subscription", cfg.Members[0].Adapter, p.ID)
		}
	}

	require.NoError(t, s.ApplyPreset(ctx, "anthropic-subscription", "", "owner@x.com"))
	panel, err := s.Panel(ctx)
	require.NoError(t, err)
	sub := panel.Members[0].ProviderID
	err = s.SetPanel(ctx, providers.Panel{Judge: providers.Slot{ProviderID: sub, Model: "sonnet"}, Members: panel.Members}, "owner@x.com")
	require.ErrorContains(t, err, "cannot judge")

	require.ErrorContains(t, s.Delete(ctx, sub, "owner@x.com"), "uses this provider")

	bad := providers.Provider{Name: "Bad", Adapter: "gateway", BaseURL: "https://openrouter.ai/api/v1", CredentialEnv: "X"}
	require.ErrorContains(t, s.Save(ctx, &bad, "owner@x.com"), "leave /v1 off")
}
