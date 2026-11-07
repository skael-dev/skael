package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/skael-dev/skael/cli/client"
	"github.com/skael-dev/skael/internal/eval/provider"
	"github.com/skael-dev/skael/internal/evalqueue"
	"github.com/skael-dev/skael/internal/workers"
)

// verifyEvery bounds how often a worker calls each provider's model list.
const verifyEvery = 10 * time.Minute

// checkin reports this worker's state to the server on each poll.
type checkin struct {
	c      *client.Client
	base   workers.WorkersCheckin
	env    func(string) string
	http   *http.Client
	anthro string

	mu        sync.Mutex
	providers []evalqueue.EvalBackend
	verified  map[string]workers.Verification
	checkedAt map[string]time.Time
}

func newCheckin(cfg workerConfig) *checkin {
	pinned := ""
	if provider.PinnedByEnv(os.Getenv) {
		pinned = cfg.Provider.Detail
	}
	return &checkin{
		c: client.New(cfg.Endpoint, cfg.APIKey),
		base: workers.WorkersCheckin{
			WorkerID: cfg.WorkerID, Version: version, Driver: cfg.SandboxCfg.Driver,
			HardwareIsolated: sandboxHardwareIsolated(cfg), NetworkPolicy: sandboxNetworkPolicy(cfg),
			Concurrency: cfg.Concurrency, SandboxImage: cfg.SandboxCfg.ImageIdentity(), Pinned: pinned,
		},
		env:       os.Getenv,
		http:      &http.Client{Timeout: 15 * time.Second},
		anthro:    "https://api.anthropic.com",
		verified:  map[string]workers.Verification{},
		checkedAt: map[string]time.Time{},
	}
}

func sandboxHardwareIsolated(cfg workerConfig) bool {
	switch cfg.SandboxCfg.Driver {
	case "kubernetes":
		return cfg.SandboxCfg.K8s.HardwareIsolated
	case "northflank":
		return cfg.SandboxCfg.NF.HardwareIsolated
	}
	return false
}

func sandboxNetworkPolicy(cfg workerConfig) bool {
	switch cfg.SandboxCfg.Driver {
	case "kubernetes":
		return cfg.SandboxCfg.K8s.NetworkPolicyEnforced
	case "northflank":
		return cfg.SandboxCfg.NF.NetworkPolicyEnforced
	}
	return false
}

// send reports once. A failure is logged and never stops the worker.
func (k *checkin) send(ctx context.Context) {
	k.mu.Lock()
	body := k.base
	names := []string{provider.APIKeyEnv, provider.AuthTokenEnv, provider.OAuthTokenEnv}
	for _, p := range k.providers {
		names = append(names, p.CredentialEnv)
	}
	for _, n := range names {
		if k.env(n) != "" && !slices.Contains(body.Credentials, n) {
			body.Credentials = append(body.Credentials, n)
		}
	}
	for _, p := range k.providers {
		if v, ok := k.verified[p.Provider]; ok {
			body.Verifications = append(body.Verifications, v)
		}
	}
	k.mu.Unlock()

	var reply workers.WorkersCheckinReply
	if err := k.c.WorkerCheckin(body, &reply); err != nil {
		log.Warn().Err(err).Msg("skael-worker: check-in failed")
		return
	}
	k.mu.Lock()
	k.providers = reply.Providers
	k.mu.Unlock()
	k.verify(ctx, reply.Providers)
}

// verify checks each provider's credential and lists its models, at most once
// per verifyEvery for an unchanged provider.
func (k *checkin) verify(ctx context.Context, list []evalqueue.EvalBackend) {
	for _, p := range list {
		key := p.Provider + "\x00" + p.Adapter + "\x00" + p.BaseURL + "\x00" + p.CredentialEnv
		k.mu.Lock()
		last, ok := k.checkedAt[key]
		k.mu.Unlock()
		if ok && time.Since(last) < verifyEvery {
			continue
		}
		v := k.verifyOne(ctx, p)
		k.mu.Lock()
		k.verified[p.Provider], k.checkedAt[key] = v, time.Now()
		k.mu.Unlock()
	}
}

func (k *checkin) verifyOne(ctx context.Context, p evalqueue.EvalBackend) workers.Verification {
	v := workers.Verification{Provider: p.Provider, Models: []string{}}
	cred := k.env(p.CredentialEnv)
	if cred == "" {
		v.Detail = "this worker has no " + p.CredentialEnv
		return v
	}
	if p.Adapter == evalqueue.AdapterSubscription {
		v.OK, v.Detail, v.Models = true, "token present; a subscription serves Claude Code's aliases", []string{"opus", "sonnet", "haiku"}
		return v
	}
	base := k.anthro
	if p.Adapter == evalqueue.AdapterGateway {
		base = p.BaseURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(base, "/")+"/v1/models", nil)
	if err != nil {
		v.Detail = err.Error()
		return v
	}
	if p.AuthStyle == "bearer" {
		req.Header.Set("Authorization", "Bearer "+cred)
	} else {
		req.Header.Set("x-api-key", cred)
	}
	req.Header.Set("anthropic-version", "2023-06-01")
	resp, err := k.http.Do(req)
	if err != nil {
		v.Detail = "unreachable: " + err.Error()
		return v
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		v.Detail = fmt.Sprintf("%s was refused (HTTP %d)", p.CredentialEnv, resp.StatusCode)
		return v
	}
	if resp.StatusCode != http.StatusOK {
		v.OK, v.Detail = true, fmt.Sprintf("reachable, but its model list returned HTTP %d; type model ids by hand", resp.StatusCode)
		return v
	}
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		v.OK, v.Detail = true, "reachable, but its model list is not JSON"
		return v
	}
	for _, m := range body.Data {
		if len(v.Models) < 300 {
			v.Models = append(v.Models, m.ID)
		}
	}
	v.OK, v.Detail = true, fmt.Sprintf("reachable; %d models listed", len(body.Data))
	return v
}

// loop checks in now and on every interval, during a job too, so a long
// evaluation never shows the worker as offline.
func (k *checkin) loop(ctx context.Context, every time.Duration) {
	if every <= 0 || every > time.Minute {
		every = time.Minute
	}
	for {
		k.send(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
}
