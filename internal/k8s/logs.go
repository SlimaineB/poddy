package k8s

import (
	"context"
	"io"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

const (
	defaultLogTailLines = int64(500)
	maxLogTailLines     = int64(2000)
	maxLogBytes         = int64(512 * 1024)
)

type ContainerLog struct {
	Name         string     `json:"name"`
	Type         string     `json:"type"`
	Image        string     `json:"image,omitempty"`
	Ready        bool       `json:"ready"`
	RestartCount int32      `json:"restartCount"`
	State        string     `json:"state"`
	Reason       string     `json:"reason,omitempty"`
	Message      string     `json:"message,omitempty"`
	StartedAt    *time.Time `json:"startedAt,omitempty"`
	FinishedAt   *time.Time `json:"finishedAt,omitempty"`
	ExitCode     *int32     `json:"exitCode,omitempty"`
	Logs         string     `json:"logs"`
	Error        string     `json:"error,omitempty"`
}

type WorkspaceLogs struct {
	Name       string         `json:"name"`
	Phase      string         `json:"phase"`
	PodPhase   string         `json:"podPhase,omitempty"`
	Template   string         `json:"template,omitempty"`
	Message    string         `json:"message,omitempty"`
	Containers []ContainerLog `json:"containers"`
}

// Logs returns the startup output for the workspace pod. Init containers and
// regular containers are both included; a failure to read one container does
// not fail the whole response.
func (m *Manager) Logs(ctx context.Context, name string, tail int64) (WorkspaceLogs, error) {
	tail = clampTail(tail)
	pod, err := m.Get(ctx, name)
	if apierrors.IsNotFound(err) {
		ws, stateErr := m.GetWorkspace(ctx, name)
		if stateErr != nil {
			return WorkspaceLogs{}, stateErr
		}
		return WorkspaceLogs{
			Name:       ws.Name,
			Phase:      ws.Phase,
			Template:   ws.Template,
			Message:    "Workspace is stopped. Start it to see startup logs.",
			Containers: []ContainerLog{},
		}, nil
	}
	if err != nil {
		return WorkspaceLogs{}, err
	}

	ws := toWorkspace(pod)
	result := WorkspaceLogs{
		Name:       ws.Name,
		Phase:      ws.Phase,
		PodPhase:   string(pod.Status.Phase),
		Template:   ws.Template,
		Containers: make([]ContainerLog, 0, len(pod.Spec.InitContainers)+len(pod.Spec.Containers)),
	}
	for _, container := range pod.Spec.InitContainers {
		status, ok := findContainerStatus(pod.Status.InitContainerStatuses, container.Name)
		result.Containers = append(result.Containers, m.containerLog(ctx, pod, container, "init", status, ok, tail))
	}
	for _, container := range pod.Spec.Containers {
		status, ok := findContainerStatus(pod.Status.ContainerStatuses, container.Name)
		result.Containers = append(result.Containers, m.containerLog(ctx, pod, container, "container", status, ok, tail))
	}
	return result, nil
}

func (m *Manager) containerLog(ctx context.Context, pod *corev1.Pod, container corev1.Container, kind string, status corev1.ContainerStatus, hasStatus bool, tail int64) ContainerLog {
	entry := ContainerLog{
		Name:  container.Name,
		Type:  kind,
		Image: container.Image,
		State: "Unknown",
	}
	if hasStatus {
		entry.Image = status.Image
		entry.Ready = status.Ready
		entry.RestartCount = status.RestartCount
		entry.State, entry.Reason, entry.Message, entry.StartedAt, entry.FinishedAt, entry.ExitCode = containerState(status)
	}

	limit := maxLogBytes
	options := &corev1.PodLogOptions{
		Container:  container.Name,
		TailLines:  &tail,
		LimitBytes: &limit,
	}
	stream, err := m.Client.CoreV1().Pods(m.Namespace).GetLogs(pod.Name, options).Stream(ctx)
	if err != nil {
		entry.Error = err.Error()
		return entry
	}
	defer stream.Close()

	data, err := io.ReadAll(stream)
	if err != nil {
		entry.Error = err.Error()
		return entry
	}
	entry.Logs = string(data)
	return entry
}

func findContainerStatus(statuses []corev1.ContainerStatus, name string) (corev1.ContainerStatus, bool) {
	for _, status := range statuses {
		if status.Name == name {
			return status, true
		}
	}
	return corev1.ContainerStatus{}, false
}

func containerState(status corev1.ContainerStatus) (string, string, string, *time.Time, *time.Time, *int32) {
	switch {
	case status.State.Waiting != nil:
		return "Waiting", status.State.Waiting.Reason, status.State.Waiting.Message, nil, nil, nil
	case status.State.Running != nil:
		started := status.State.Running.StartedAt.Time
		return "Running", "", "", &started, nil, nil
	case status.State.Terminated != nil:
		terminated := status.State.Terminated
		started := terminated.StartedAt.Time
		finished := terminated.FinishedAt.Time
		exitCode := terminated.ExitCode
		return "Terminated", terminated.Reason, terminated.Message, &started, &finished, &exitCode
	default:
		return "Unknown", "", "", nil, nil, nil
	}
}

func clampTail(tail int64) int64 {
	if tail <= 0 {
		return defaultLogTailLines
	}
	if tail > maxLogTailLines {
		return maxLogTailLines
	}
	return tail
}
