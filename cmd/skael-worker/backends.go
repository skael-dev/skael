package main

import (
	"errors"
	"fmt"

	"github.com/skael-dev/skael/internal/eval/agent"
	"github.com/skael-dev/skael/internal/eval/llm"
	"github.com/skael-dev/skael/internal/eval/provider"
	"github.com/skael-dev/skael/internal/evalqueue"
)

// errNoPanel is a job claimed while the server has no judge and panel.
var errNoPanel = errors.New("skael-worker: no judge and panel are configured on the server; add a provider in Settings → Evaluation")

// jobBackends is one job's judge gateway and panel, built from the claim.
type jobBackends struct {
	gateway        llm.Gateway
	panelModels    []string
	panelBase      string
	memberAuth     func(agent, model string) ([]string, bool)
	memberProvider func(agent, model string) string
}

// forJob builds the judge and each member's session environment from the
// server's providers, keys included. The worker reads no LLM settings of its
// own.
func forJob(eval *evalqueue.EvalConfig) (jobBackends, error) {
	if eval == nil || len(eval.Members) == 0 {
		return jobBackends{}, errNoPanel
	}
	gw, err := provider.Judge(eval.Judge, judgeGatewayOptions())
	if err != nil {
		return jobBackends{}, fmt.Errorf("skael-worker: judge on %s: %w", eval.Judge.Provider, err)
	}
	envs := map[string][]string{}
	labels := map[string]string{}
	models := make([]string, 0, len(eval.Members))
	for _, m := range eval.Members {
		if m.Credential == "" {
			return jobBackends{}, fmt.Errorf("skael-worker: panel member %s: the provider %s has no key; add one in Settings → Evaluation", m.Model, m.Provider)
		}
		e, err := agent.SessionEnv(provider.Session(m))
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
