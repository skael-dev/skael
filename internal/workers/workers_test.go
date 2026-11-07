package workers_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skael-dev/skael/internal/evalqueue"
	"github.com/skael-dev/skael/internal/workers"
)

func TestReadiness_NamesTheMissingCredentialAndTheOddImage(t *testing.T) {
	now := time.Now()
	cfg := &evalqueue.EvalConfig{
		Judge:   evalqueue.EvalBackend{Provider: "Anthropic", CredentialEnv: "ANTHROPIC_API_KEY"},
		Members: []evalqueue.EvalBackend{{Provider: "Claude subscription", CredentialEnv: "CLAUDE_CODE_OAUTH_TOKEN"}},
	}
	w := func(id, image string, creds ...string) workers.Worker {
		return workers.Worker{WorkersCheckin: workers.WorkersCheckin{WorkerID: id, SandboxImage: image, Credentials: creds}, LastSeen: now}
	}
	list := []workers.Worker{
		w("a", "base@1", "ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN"),
		w("b", "base@1", "ANTHROPIC_API_KEY"),
		w("c", "base@2", "ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN"),
		{WorkersCheckin: workers.WorkersCheckin{WorkerID: "d"}, LastSeen: now.Add(-time.Hour)},
	}
	workers.Readiness(list, cfg, now)

	require.Empty(t, list[0].Problems)
	require.Equal(t, []string{"missing CLAUDE_CODE_OAUTH_TOKEN for the provider Claude subscription"}, list[1].Problems)
	require.Len(t, list[2].Problems, 1)
	require.Contains(t, list[2].Problems[0], "sandbox image base@2 differs")
	require.Contains(t, list[3].Problems[0], "offline")
}
