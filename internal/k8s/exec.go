package k8s

import (
	"bytes"
	"context"
	"net/http"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
)

func (m *Manager) ExecRequest(name string, command []string) *rest.Request {
	return m.execRequest(name, command, true)
}

func (m *Manager) execRequest(name string, command []string, tty bool) *rest.Request {
	return m.Client.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(PodName(name)).
		Namespace(m.Namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: ContainerName,
			Command:   command,
			Stdin:     tty,
			Stdout:    true,
			Stderr:    true,
			TTY:       tty,
		}, scheme.ParameterCodec)
}

// Exec runs a command in the workspace container and returns its output. It is
// used for short, non-interactive probes such as listing listening ports.
func (m *Manager) Exec(ctx context.Context, name string, command []string) ([]byte, []byte, error) {
	executor, err := remotecommand.NewSPDYExecutor(m.Rest, http.MethodPost, m.execRequest(name, command, false).URL())
	if err != nil {
		return nil, nil, err
	}

	var stdout, stderr bytes.Buffer
	err = executor.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: &stdout,
		Stderr: &stderr,
	})
	return stdout.Bytes(), stderr.Bytes(), err
}
