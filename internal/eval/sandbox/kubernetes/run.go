package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/skael-dev/skael/internal/eval/sandbox"
)

// defaultWaitInterval paces the readiness poll. run_test.go shortens it.
const defaultWaitInterval = 500 * time.Millisecond

// Run executes one command in a fresh pod. A non-zero exit is a result, not an
// error, matching the Docker driver.
func (d *Driver) Run(ctx context.Context, rs sandbox.RunSpec) (sandbox.RunResult, error) {
	n, err := newNames()
	if err != nil {
		return sandbox.RunResult{}, err
	}
	pod, err := SessionPod(rs, d.o, n)
	if err != nil {
		return sandbox.RunResult{}, err
	}

	cleanup, err := d.prepareNetwork(ctx, rs, n)
	if err != nil {
		return sandbox.RunResult{}, err
	}
	defer cleanup()

	if _, err := d.cs.CoreV1().Pods(d.o.Namespace).Create(ctx, pod, metav1.CreateOptions{}); err != nil {
		return sandbox.RunResult{}, fmt.Errorf("kubernetes: creating the session pod: %w", err)
	}
	defer d.deletePod(n.Session)

	if err := d.waitRunning(ctx, n.Session, rs.Timeout); err != nil {
		return d.classify(ctx, sandbox.RunResult{}, err)
	}

	workdir := rs.WorkDir
	if workdir == "" {
		workdir = sandbox.DefaultWorkDir
	}
	if err := d.stageIn(ctx, n.Session, workdir, rs.Workspace); err != nil {
		return d.classify(ctx, sandbox.RunResult{}, err)
	}

	runCtx, cancel := context.WithTimeout(ctx, rs.Timeout)
	defer cancel()

	start := time.Now()
	code, execErr := d.ex.Exec(runCtx, execRequest{
		Pod: n.Session, Container: "session", Argv: rs.Argv,
		Stdin: rs.Stdin, Stdout: rs.Stdout, Stderr: rs.Stderr,
	})
	res := sandbox.RunResult{ExitCode: code, Duration: time.Since(start)}

	switch {
	case errors.Is(runCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil:
		res.TimedOut = true
		return res, nil
	case ctx.Err() != nil:
		res.Cancelled = true
		return res, fmt.Errorf("kubernetes: run cancelled: %w", ctx.Err())
	case execErr != nil:
		return res, execErr
	}

	// The workspace is collected on a context of its own: the run is over, and
	// its outputs are the only record of what happened.
	outCtx, outCancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
	defer outCancel()
	if err := d.collectOut(outCtx, n.Session, workdir, rs.Workspace); err != nil {
		return res, err
	}
	return res, nil
}

// prepareNetwork creates the policy and, for an allowlist, the proxy. The
// returned cleanup removes whatever was created.
func (d *Driver) prepareNetwork(ctx context.Context, rs sandbox.RunSpec, n names) (func(), error) {
	if err := d.o.CheckNetwork(rs.Network); err != nil {
		return nil, err
	}
	noop := func() {}
	if rs.Network == sandbox.NetFull {
		return noop, nil
	}

	policy := DenyAllEgressPolicy(d.o, n)
	if rs.Network == sandbox.NetAllowlist {
		cm, err := ProxyConfigMap(rs.Allow, d.o, n)
		if err != nil {
			return nil, err
		}
		if _, err := d.cs.CoreV1().ConfigMaps(d.o.Namespace).Create(ctx, cm, metav1.CreateOptions{}); err != nil {
			return nil, fmt.Errorf("kubernetes: creating the proxy config: %w", err)
		}
		if _, err := d.cs.CoreV1().Pods(d.o.Namespace).Create(ctx, ProxyPod(d.o, n), metav1.CreateOptions{}); err != nil {
			d.deleteConfigMap(n.ConfigMap)
			return nil, fmt.Errorf("kubernetes: creating the proxy pod: %w", err)
		}
		policy = EgressPolicy(d.o, n)
	}

	if _, err := d.cs.NetworkingV1().NetworkPolicies(d.o.Namespace).Create(ctx, policy, metav1.CreateOptions{}); err != nil {
		d.deletePod(n.Proxy)
		d.deleteConfigMap(n.ConfigMap)
		return nil, fmt.Errorf("kubernetes: creating the egress policy: %w", err)
	}

	if rs.Network == sandbox.NetAllowlist {
		if err := d.waitRunning(ctx, n.Proxy, time.Minute); err != nil {
			d.deletePolicy(n.Policy)
			d.deletePod(n.Proxy)
			d.deleteConfigMap(n.ConfigMap)
			return nil, fmt.Errorf("kubernetes: the proxy never became ready: %w", err)
		}
	}

	return func() {
		d.deletePolicy(n.Policy)
		d.deletePod(n.Proxy)
		d.deleteConfigMap(n.ConfigMap)
	}, nil
}

func (d *Driver) waitRunning(ctx context.Context, name string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		pod, err := d.cs.CoreV1().Pods(d.o.Namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		switch pod.Status.Phase {
		case corev1.PodRunning:
			return nil
		case corev1.PodFailed, corev1.PodSucceeded:
			return fmt.Errorf("kubernetes: pod %s reached %s before it could be used: %s", name, pod.Status.Phase, pod.Status.Reason)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("kubernetes: pod %s was still %s after %s", name, pod.Status.Phase, timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(d.waitInterval):
		}
	}
}

// classify turns a setup failure into the right RunResult. A cancelled run
// must never be recorded as a failed one.
func (d *Driver) classify(ctx context.Context, res sandbox.RunResult, err error) (sandbox.RunResult, error) {
	if ctx.Err() != nil {
		res.Cancelled = true
	}
	return res, err
}

// Deletions run on a context of their own: the run's context is usually
// already cancelled by the time they matter, and a leaked pod holds capacity.
func (d *Driver) deletePod(name string) {
	if name == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	grace := int64(0)
	_ = d.cs.CoreV1().Pods(d.o.Namespace).Delete(ctx, name, metav1.DeleteOptions{GracePeriodSeconds: &grace})
}

func (d *Driver) deleteConfigMap(name string) {
	if name == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = d.cs.CoreV1().ConfigMaps(d.o.Namespace).Delete(ctx, name, metav1.DeleteOptions{})
}

func (d *Driver) deletePolicy(name string) {
	if name == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = d.cs.NetworkingV1().NetworkPolicies(d.o.Namespace).Delete(ctx, name, metav1.DeleteOptions{})
}
