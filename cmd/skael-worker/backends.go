package main

import (
	"fmt"
	"os"
	"sync"

	"github.com/skael-dev/skael/internal/eval/llm"
	"github.com/skael-dev/skael/internal/eval/provider"
	"github.com/skael-dev/skael/internal/evalqueue"
)

// backends picks the judge and the panel for each job: the server's providers,
// unless this worker's env pins its own gateway or panel.
type backends struct {
	legacy provider.Config
	pinned bool
	env    provider.Getenv

	mu       sync.Mutex
	legacyGW llm.Gateway
}

func newBackends(legacy provider.Config) *backends {
	return &backends{legacy: legacy, pinned: provider.PinnedByEnv(os.Getenv), env: os.Getenv}
}

// jobBackends is one job's judge gateway and panel credentials.
type jobBackends struct {
	gateway         llm.Gateway
	panelModels     []string
	panelBase       string
	panelExcludeEnv []string
	memberAuth      func(agent, model string) ([]string, bool)
	memberProvider  func(agent, model string) string
}

func (b *backends) forJob(eval *evalqueue.EvalConfig) (jobBackends, error) {
	if b.pinned || eval == nil || len(eval.Members) == 0 {
		return b.legacyJob()
	}
	judge, err := provider.Judge(b.env, toBackend(eval.Judge))
	if err != nil {
		return jobBackends{}, fmt.Errorf("skael-worker: judge %s: %w", eval.Judge.Provider, err)
	}
	gw, err := judge.Gateway(judgeGatewayOptions())
	if err != nil {
		return jobBackends{}, fmt.Errorf("skael-worker: judge %s: %w", eval.Judge.Provider, err)
	}
	envs := map[string][]string{}
	labels := map[string]string{}
	models := make([]string, 0, len(eval.Members))
	for _, m := range eval.Members {
		e, err := provider.MemberEnv(b.env, toBackend(m))
		if err != nil {
			return jobBackends{}, fmt.Errorf("skael-worker: panel member %s on %s: %w", m.Model, m.Provider, err)
		}
		envs[m.Model], labels[m.Model] = e, m.Label()
		models = append(models, m.Model)
	}
	return jobBackends{
		gateway:     gw,
		panelModels: models,
		panelBase:   eval.Members[0].BaseURL,
		memberAuth: func(_, model string) ([]string, bool) {
			e, ok := envs[model]
			return e, ok
		},
		memberProvider: func(_, model string) string { return labels[model] },
	}, nil
}

func (b *backends) legacyJob() (jobBackends, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.legacyGW == nil {
		if err := b.legacy.Validate(); err != nil {
			return jobBackends{}, fmt.Errorf("skael-worker: no provider for this job: %w. Set one up on the server's "+
				"Workers page, or set ANTHROPIC_API_KEY on this worker", err)
		}
		gw, err := b.legacy.Gateway(judgeGatewayOptions())
		if err != nil {
			return jobBackends{}, fmt.Errorf("skael-worker: LLM gateway: %w", err)
		}
		b.legacyGW = gw
	}
	return jobBackends{
		gateway:         b.legacyGW,
		panelModels:     b.legacy.PanelModels(),
		panelBase:       b.legacy.BaseURL,
		panelExcludeEnv: b.legacy.PanelExcludeEnv(),
	}, nil
}

func toBackend(b evalqueue.EvalBackend) provider.Backend {
	return provider.Backend{Adapter: b.Adapter, BaseURL: b.BaseURL, AuthStyle: b.AuthStyle, CredentialEnv: b.CredentialEnv, Model: b.Model}
}
