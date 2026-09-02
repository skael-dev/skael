package kubernetes

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	fake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/skael-dev/skael/internal/eval/sandbox"
)

// scriptedExecer stands in for a pod across the three exec calls Run makes:
// stage-in (stdin set), argv (neither stdin nor stdout piping a tar), and
// collect-out (stdout set). It reuses tarExecer's tar halves for the first
// and last, and scripts the middle one.
type scriptedExecer struct {
	remote   string
	argvCode int
	stageErr error
	block    bool
}

func (e *scriptedExecer) Exec(ctx context.Context, r execRequest) (int, error) {
	if e.block {
		<-ctx.Done()
		return 0, ctx.Err()
	}
	switch {
	case r.Stdin != nil:
		if e.stageErr != nil {
			return 1, e.stageErr
		}
		return 0, untarInto(e.remote, r.Stdin)
	case len(r.Argv) > 0 && r.Argv[0] == "tar":
		return 0, tarDir(e.remote, r.Stdout)
	default:
		return e.argvCode, nil
	}
}

// readyClientset marks every created pod Running, since nothing in the fake
// runs a kubelet.
func readyClientset() *fake.Clientset {
	cs := fake.NewSimpleClientset()
	cs.PrependReactor("create", "pods", func(a k8stesting.Action) (bool, runtime.Object, error) {
		pod := a.(k8stesting.CreateAction).GetObject().(*corev1.Pod)
		pod.Status.Phase = corev1.PodRunning
		return false, nil, nil
	})
	return cs
}

func runDriver(t *testing.T, o Options, ex execer) (*Driver, *fake.Clientset) {
	t.Helper()
	cs := readyClientset()
	d, err := New(o, cs, ex)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	d.waitInterval = time.Millisecond
	return d, cs
}

func runnableSpec(t *testing.T) sandbox.RunSpec {
	t.Helper()
	return sandbox.RunSpec{
		Image:     sandbox.ImageRef{Tag: "img"},
		Workspace: t.TempDir(),
		Argv:      []string{"claude", "-p", "go"},
		Network:   sandbox.NetFull,
		Timeout:   time.Minute,
	}
}

func TestRun_ReturnsTheCommandsExitCodeAsAResult(t *testing.T) {
	d, _ := runDriver(t, validOptions(), &scriptedExecer{remote: t.TempDir(), argvCode: 3})
	res, err := d.Run(context.Background(), runnableSpec(t))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.ExitCode != 3 {
		t.Errorf("ExitCode = %d, want 3; a non-zero exit is a result, not an error", res.ExitCode)
	}
}

// A leaked pod holds cluster capacity; a leaked ConfigMap or NetworkPolicy is
// worse, since a stale policy silently changes the *next* session's egress
// rather than erroring. Cases mix NetFull (nothing but the pod is created) and
// NetAllowlist (pod, proxy pod, ConfigMap and policy), so an assertion here
// only holds if there was something to leak.
func TestRun_DeletesEveryResourceOnEveryExitPath(t *testing.T) {
	for _, tc := range []struct {
		name string
		o    Options
		rs   func(*testing.T) sandbox.RunSpec
		ex   execer
	}{
		{"clean exit", validOptions(), runnableSpec, &scriptedExecer{remote: t.TempDir()}},
		{"argv fails", validOptions(), runnableSpec, &scriptedExecer{remote: t.TempDir(), argvCode: 1}},
		{"stage-in fails", validOptions(), runnableSpec, &scriptedExecer{remote: t.TempDir(), stageErr: errors.New("no")}},
		{"allowlist clean exit", enforcedOptions(), allowlistSpec, &scriptedExecer{remote: t.TempDir()}},
		{"allowlist stage-in fails", enforcedOptions(), allowlistSpec, &scriptedExecer{remote: t.TempDir(), stageErr: errors.New("no")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, cs := runDriver(t, tc.o, tc.ex)
			_, _ = d.Run(context.Background(), tc.rs(t))
			ns := validOptions().Namespace

			pods, err := cs.CoreV1().Pods(ns).List(context.Background(), metav1.ListOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if len(pods.Items) != 0 {
				t.Errorf("%d pods left behind", len(pods.Items))
			}

			cms, err := cs.CoreV1().ConfigMaps(ns).List(context.Background(), metav1.ListOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if len(cms.Items) != 0 {
				t.Errorf("%d config maps left behind", len(cms.Items))
			}

			policies, err := cs.NetworkingV1().NetworkPolicies(ns).List(context.Background(), metav1.ListOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if len(policies.Items) != 0 {
				t.Errorf("%d network policies left behind", len(policies.Items))
			}
		})
	}
}

// allowlistSpec is runnableSpec with NetAllowlist, so prepareNetwork creates
// the proxy pod, its ConfigMap and the NetworkPolicy for the leak check above
// to have something to find.
func allowlistSpec(t *testing.T) sandbox.RunSpec {
	rs := runnableSpec(t)
	rs.Network, rs.Allow = sandbox.NetAllowlist, []string{"api.anthropic.com"}
	return rs
}

func TestRun_CreatesTheProxyAndThePolicyForAnAllowlistRun(t *testing.T) {
	o := validOptions()
	o.NetworkPolicyEnforced = true
	d, cs := runDriver(t, o, &scriptedExecer{remote: t.TempDir()})

	rs := runnableSpec(t)
	rs.Network, rs.Allow = sandbox.NetAllowlist, []string{"api.anthropic.com"}
	if _, err := d.Run(context.Background(), rs); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var createdPods, createdPolicies int
	for _, a := range cs.Actions() {
		if a.GetVerb() != "create" {
			continue
		}
		switch a.GetResource().Resource {
		case "pods":
			createdPods++
		case "networkpolicies":
			createdPolicies++
		}
	}
	if createdPods != 2 {
		t.Errorf("created %d pods, want 2 (session and proxy)", createdPods)
	}
	if createdPolicies != 1 {
		t.Errorf("created %d network policies, want 1", createdPolicies)
	}
}

func TestRun_RefusesARestrictedRunWithoutTheEnforcementAssertion(t *testing.T) {
	d, _ := runDriver(t, validOptions(), &scriptedExecer{remote: t.TempDir()})
	rs := runnableSpec(t)
	rs.Network = sandbox.NetNone
	_, err := d.Run(context.Background(), rs)
	if !errors.Is(err, ErrNetworkPolicyUnenforced) {
		t.Fatalf("Run = %v, want ErrNetworkPolicyUnenforced", err)
	}
}

func TestRun_MarksACancelledRunCancelledRatherThanFailed(t *testing.T) {
	d, _ := runDriver(t, validOptions(), &scriptedExecer{remote: t.TempDir(), block: true})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, err := d.Run(ctx, runnableSpec(t))
	if err == nil {
		t.Fatal("Run: want an error for a cancelled run")
	}
	if !res.Cancelled {
		t.Error("Cancelled must be true; a cancelled run must never record as a failure")
	}
}

func TestRun_RefusesAHostMountWithAMessageNamingTheAlternative(t *testing.T) {
	d, _ := runDriver(t, validOptions(), &scriptedExecer{remote: t.TempDir()})
	rs := runnableSpec(t)
	rs.Mounts = []sandbox.Mount{{HostPath: "/home/u/.claude", ContainerPath: "/home/runner/.claude", ReadOnly: true}}
	_, err := d.Run(context.Background(), rs)
	if err == nil || !strings.Contains(err.Error(), "CLAUDE_CODE_OAUTH_TOKEN") {
		t.Fatalf("Run = %v, want a refusal naming CLAUDE_CODE_OAUTH_TOKEN", err)
	}
}
