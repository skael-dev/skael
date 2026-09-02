package main

import (
	"strings"
	"testing"

	"github.com/skael-dev/skael/internal/eval/sandbox/resolve"
)

// The guard exists because the failure it prevents is silent: Docker creates a
// missing bind source as an empty directory rather than refusing, so a
// containerized worker without these roots posts real-looking scores for
// sandboxes that never received a task, a skill, or a verifier.
func TestRequireHostSharedRoots(t *testing.T) {
	const run, work = "/var/lib/skael/run", "/var/lib/skael/work"

	tests := []struct {
		name          string
		runRoot       string
		workRoot      string
		containerized bool
		wantErr       bool
		wantNamed     []string
	}{
		{name: "on the host, unset is correct", containerized: false},
		{name: "on the host, set is allowed", runRoot: run, workRoot: work, containerized: false},
		{name: "in a container, both set is the supported setup", runRoot: run, workRoot: work, containerized: true},
		{
			name:     "in a container, a missing run root must not start",
			workRoot: work, containerized: true,
			wantErr: true, wantNamed: []string{"WORKER_RUN_ROOT"},
		},
		{
			// The verifier is bind-mounted from under the work root, so this
			// is just as fatal as a missing run root and just as silent.
			name:    "in a container, a missing work root must not start",
			runRoot: run, containerized: true,
			wantErr: true, wantNamed: []string{"WORKER_WORK_ROOT"},
		},
		{
			name:          "in a container, both missing names both",
			containerized: true,
			wantErr:       true, wantNamed: []string{"WORKER_RUN_ROOT", "WORKER_WORK_ROOT"},
		},
	}

	dockerCfg := resolve.FromEnv(func(string) string { return "" })

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := requireHostSharedRoots(dockerCfg, tc.runRoot, tc.workRoot, tc.containerized)
			if (err != nil) != tc.wantErr {
				t.Fatalf("requireHostSharedRoots(%q, %q, %v) error = %v, wantErr %v",
					tc.runRoot, tc.workRoot, tc.containerized, err, tc.wantErr)
			}
			// An operator can only fix what the message names.
			for _, name := range tc.wantNamed {
				if !strings.Contains(err.Error(), name) {
					t.Errorf("error does not name %s: %v", name, err)
				}
			}
		})
	}
}

// A containerized docker worker still needs the shared roots: its sandboxes
// are bind-mounted and the host daemon resolves those paths. A kubernetes
// worker mounts nothing, so requiring them would block a valid deployment.
func TestRequireHostSharedRoots_AppliesToDockerOnly(t *testing.T) {
	dockerCfg := resolve.FromEnv(func(string) string { return "" })
	if err := requireHostSharedRoots(dockerCfg, "", "", true); err == nil {
		t.Error("a containerized docker worker without WORKER_RUN_ROOT must be refused")
	}

	k8sCfg := resolve.FromEnv(func(k string) string {
		switch k {
		case "SANDBOX_DRIVER":
			return "kubernetes"
		case "SANDBOX_K8S_NAMESPACE":
			return "skael-sandbox"
		case "SANDBOX_K8S_IMAGE":
			return "img"
		}
		return ""
	})
	if err := requireHostSharedRoots(k8sCfg, "", "", true); err != nil {
		t.Errorf("a containerized kubernetes worker needs no shared roots, got %v", err)
	}
}
