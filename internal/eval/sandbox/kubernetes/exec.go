package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"io"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8s "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	utilexec "k8s.io/client-go/util/exec"
)

type execRequest struct {
	Pod       string
	Container string
	Argv      []string
	Stdin     io.Reader
	Stdout    io.Writer
	Stderr    io.Writer
}

// execer runs one command in a running pod. It is an interface because
// client-go's fake clientset does not serve the exec subresource, and because
// the workspace mirror is worth testing without a cluster.
type execer interface {
	Exec(ctx context.Context, r execRequest) (int, error)
}

type apiExecer struct {
	cs        k8s.Interface
	cfg       *rest.Config
	namespace string
}

// Exec returns the command's exit code. A non-zero exit is a result, not an
// error: only a transport failure is.
func (e *apiExecer) Exec(ctx context.Context, r execRequest) (int, error) {
	req := e.cs.CoreV1().RESTClient().Post().
		Resource("pods").Name(r.Pod).Namespace(e.namespace).SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: r.Container,
			Command:   r.Argv,
			Stdin:     r.Stdin != nil,
			Stdout:    r.Stdout != nil,
			Stderr:    r.Stderr != nil,
		}, runtime.NewParameterCodec(scheme.Scheme))

	x, err := remotecommand.NewSPDYExecutor(e.cfg, "POST", req.URL())
	if err != nil {
		return 0, fmt.Errorf("kubernetes: exec in %s: %w", r.Pod, err)
	}
	err = x.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdin: r.Stdin, Stdout: r.Stdout, Stderr: r.Stderr,
	})
	if err == nil {
		return 0, nil
	}
	var ce utilexec.CodeExitError
	if errors.As(err, &ce) {
		return ce.ExitStatus(), nil
	}
	return 0, fmt.Errorf("kubernetes: exec in %s: %w", r.Pod, err)
}
