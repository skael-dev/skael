package main

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/skael-dev/skael/cli/client"
	"github.com/skael-dev/skael/internal/eval/agent"
	"github.com/skael-dev/skael/internal/workers"
)

// checkin reports this worker's state to the server on each poll. Provider
// keys live on the server, so it reports none.
type checkin struct {
	c    *client.Client
	base workers.WorkersCheckin
}

func newCheckin(cfg workerConfig) *checkin {
	return &checkin{
		c: client.New(cfg.Endpoint, cfg.APIKey),
		base: workers.WorkersCheckin{
			WorkerID: cfg.WorkerID, Version: version, Driver: cfg.SandboxCfg.Driver,
			HardwareIsolated: sandboxHardwareIsolated(cfg), NetworkPolicy: sandboxNetworkPolicy(cfg),
			Concurrency: cfg.Concurrency, SandboxImage: cfg.SandboxCfg.ImageIdentity(),
			Harness: agent.New().Name() + " " + agent.OpenCodeVersion,
		},
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
func (k *checkin) send() {
	var reply workers.WorkersCheckinReply
	if err := k.c.WorkerCheckin(k.base, &reply); err != nil {
		log.Warn().Err(err).Msg("skael-worker: check-in failed")
	}
}

// loop checks in now and on every interval, during a job too, so a long
// evaluation never shows the worker as offline.
func (k *checkin) loop(ctx context.Context, every time.Duration) {
	if every <= 0 || every > time.Minute {
		every = time.Minute
	}
	for {
		k.send()
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
}
