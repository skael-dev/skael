package platform_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"

	"github.com/skael-dev/skael/internal/platform"
	"github.com/skael-dev/skael/internal/testutil"
)

// A v0.19 instance's providers survive the move to server-held keys: each
// arrives with no key, a subscription and any panel using it go, and a gateway
// becomes the type its URL names.
func TestMigration040MovesProvidersToServerKeys(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	pool := testutil.SetupTestDBAtVersion(t, 39)
	ctx := context.Background()

	var anthropic, openrouter, litellm, sub string
	insert := func(name, adapter, base, env string, id *string) {
		require.NoError(t, pool.QueryRow(ctx, `INSERT INTO providers (name, adapter, base_url, auth_style, credential_env, updated_by)
			VALUES ($1, $2, $3, 'bearer', $4, 'o') RETURNING id`, name, adapter, base, env).Scan(id))
	}
	insert("Anthropic", "anthropic", "", "ANTHROPIC_API_KEY", &anthropic)
	insert("OpenRouter", "gateway", "https://openrouter.ai/api", "OPENROUTER_API_KEY", &openrouter)
	insert("LiteLLM", "gateway", "http://litellm:4000", "LITELLM_API_KEY", &litellm)
	insert("Claude subscription", "subscription", "", "CLAUDE_CODE_OAUTH_TOKEN", &sub)
	_, err := pool.Exec(ctx, `INSERT INTO eval_panel (judge_provider, judge_model, members, updated_by)
		VALUES ($1, 'claude-opus-5', jsonb_build_array(jsonb_build_object('provider_id', $2::text, 'model', 'sonnet')), 'o')`, anthropic, sub)
	require.NoError(t, err)

	db := stdlib.OpenDBFromPool(pool)
	defer db.Close() //nolint:errcheck
	require.NoError(t, platform.MigrateUpTo(db, 40))

	types := map[string]string{}
	rows, err := pool.Query(ctx, `SELECT name, type, base_url, credential_sealed, credential_ref FROM providers`)
	require.NoError(t, err)
	for rows.Next() {
		var name, typ, base, sealed, ref string
		require.NoError(t, rows.Scan(&name, &typ, &base, &sealed, &ref))
		types[name] = typ + " " + base
		require.Empty(t, sealed+ref, "%s must arrive with no key", name)
	}
	require.NoError(t, rows.Err())
	require.Equal(t, map[string]string{
		"Anthropic":  "anthropic ",
		"OpenRouter": "openrouter ",
		"LiteLLM":    "anthropic http://litellm:4000",
	}, types)

	var panels int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM eval_panel`).Scan(&panels))
	require.Zero(t, panels, "a panel that used the subscription must go with it")

	var harness string
	require.NoError(t, pool.QueryRow(ctx, `SELECT column_default FROM information_schema.columns WHERE table_name = 'workers' AND column_name = 'harness'`).Scan(&harness))
	require.NotEmpty(t, harness)
}
