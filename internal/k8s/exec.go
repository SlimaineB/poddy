package k8s

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
)

func (m *Manager) ExecRequest(name string, command []string) *rest.Request {
	return m.Client.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(PodName(name)).
		Namespace(m.Namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: ContainerName,
			Command:   command,
			Stdin:     true,
			Stdout:    true,
			Stderr:    true,
			TTY:       true,
		}, scheme.ParameterCodec)
}
