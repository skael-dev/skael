// Package kubernetes implements the sandbox driver on the Kubernetes API. It
// exists so a worker can run where there is no Docker daemon: a containerd
// node, or any cluster that will not mount a socket into a pod.
//
// Unlike the Docker driver it cannot observe its own isolation or its own
// egress enforcement, because both belong to the cluster. Where it cannot
// observe, it refuses.
package kubernetes

import (
	"encoding/json"
	"errors"
	"fmt"

	corev1 "k8s.io/api/core/v1"

	"github.com/skael-dev/skael/internal/eval/sandbox"
)

// ErrNetworkPolicyUnenforced is returned for a restricted run on a cluster
// whose CNI enforcement the operator has not asserted.
var ErrNetworkPolicyUnenforced = errors.New("kubernetes: NetworkPolicy enforcement is not asserted")

// Options configures the driver. The two booleans are operator assertions
// about the cluster, not facts the driver can check.
type Options struct {
	Namespace    string
	Image        string
	PullSecret   string
	RuntimeClass string

	// Tolerations and NodeSelector are JSON in the Kubernetes shape, kept raw
	// so the resolve package does not import k8s.io/*. Validate parses them.
	Tolerations  string
	NodeSelector string

	HardwareIsolated      bool
	NetworkPolicyEnforced bool

	CPUs   string
	Memory string

	Logger func(format string, args ...any)
}

// Validate reports an unusable configuration.
func (o Options) Validate() error {
	if o.Namespace == "" {
		return errors.New("kubernetes: no namespace; set SANDBOX_K8S_NAMESPACE to a namespace holding nothing but session pods")
	}
	if o.Image == "" {
		return errors.New("kubernetes: no image; set SANDBOX_K8S_IMAGE to the published base image reference")
	}
	if o.HardwareIsolated && o.RuntimeClass == "" {
		return errors.New("kubernetes: SANDBOX_K8S_HARDWARE_ISOLATED is set without SANDBOX_K8S_RUNTIME_CLASS; the driver cannot tell an isolated runtime from a shared-kernel one by name alone")
	}
	if _, _, err := o.scheduling(); err != nil {
		return err
	}
	return nil
}

// scheduling parses the placement both pods share.
func (o Options) scheduling() ([]corev1.Toleration, map[string]string, error) {
	var tolerations []corev1.Toleration
	if o.Tolerations != "" {
		if err := json.Unmarshal([]byte(o.Tolerations), &tolerations); err != nil {
			return nil, nil, fmt.Errorf(`kubernetes: SANDBOX_K8S_TOLERATIONS is not a JSON list of tolerations, such as [{"key":"dedicated","operator":"Equal","value":"evals","effect":"NoSchedule"}]: %w`, err)
		}
	}
	var nodeSelector map[string]string
	if o.NodeSelector != "" {
		if err := json.Unmarshal([]byte(o.NodeSelector), &nodeSelector); err != nil {
			return nil, nil, fmt.Errorf(`kubernetes: SANDBOX_K8S_NODE_SELECTOR is not a JSON object of labels, such as {"agentpool":"evals"}: %w`, err)
		}
	}
	return tolerations, nodeSelector, nil
}

// CheckNetwork refuses a policy this cluster cannot be shown to enforce.
func (o Options) CheckNetwork(p sandbox.NetworkPolicy) error {
	if p == sandbox.NetFull || o.NetworkPolicyEnforced {
		return nil
	}
	return fmt.Errorf("%w: policy %q needs a CNI that enforces NetworkPolicy. Set SANDBOX_K8S_NETWORK_POLICY=true once you have confirmed yours does; `whetstone doctor` checks it", ErrNetworkPolicyUnenforced, p)
}

func (o Options) withDefaults() Options {
	if o.CPUs == "" {
		o.CPUs = "2"
	}
	if o.Memory == "" {
		o.Memory = "4Gi"
	}
	if o.Logger == nil {
		o.Logger = func(string, ...any) {}
	}
	return o
}
