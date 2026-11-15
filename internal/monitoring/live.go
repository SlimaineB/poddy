package monitoring

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	metricsv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"
	metricsclient "k8s.io/metrics/pkg/client/clientset/versioned"
)

const (
	DefaultCacheTTL = 10 * time.Second
	errorCacheTTL   = 3 * time.Second
)

var (
	// ErrNotConfigured is returned when live monitoring is disabled or the
	// Kubernetes REST configuration is missing.
	ErrNotConfigured = errors.New("live metrics are not configured")
	// ErrNotReady is returned when metrics-server has no sample for a pod yet.
	ErrNotReady = errors.New("workspace metrics are not available yet")
)

type metricsCache struct {
	items     map[string]metricsv1beta1.PodMetrics
	expiresAt time.Time
	err       error
}

// Provider reads pod metrics from metrics-server and caches the namespace list
// so a dashboard polling several workspaces does not issue one API call per
// workspace.
type Provider struct {
	client    metricsclient.Interface
	namespace string
	ttl       time.Duration
	now       func() time.Time

	mu    sync.Mutex
	cache metricsCache
}

func NewProvider(cfg *rest.Config, namespace string, ttl time.Duration) (*Provider, error) {
	if cfg == nil {
		return nil, ErrNotConfigured
	}
	client, err := metricsclient.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("metrics client: %w", err)
	}
	return NewProviderWithClient(client, namespace, ttl), nil
}

func NewProviderWithClient(client metricsclient.Interface, namespace string, ttl time.Duration) *Provider {
	if ttl <= 0 {
		ttl = DefaultCacheTTL
	}
	return &Provider{
		client:    client,
		namespace: namespace,
		ttl:       ttl,
		now:       time.Now,
	}
}

// Workspace builds a live monitoring payload from the pod spec and the latest
// cached metrics-server sample. Declared resources and container status are
// returned even when metrics are unavailable.
func (p *Provider) Workspace(ctx context.Context, pod *corev1.Pod) WorkspaceMetrics {
	out := WorkspaceMetrics{Containers: []ContainerMetrics{}}
	if pod == nil {
		out.Reason = "workspace is not running"
		return out
	}

	out.Phase = string(pod.Status.Phase)
	out.Node = pod.Spec.NodeName
	if pod.Status.StartTime != nil {
		started := pod.Status.StartTime.Time
		out.StartedAt = &started
	}
	for _, container := range pod.Spec.Containers {
		item := ContainerMetrics{
			Name:     container.Name,
			Requests: resourcesFromList(container.Resources.Requests),
			Limits:   resourcesFromList(container.Resources.Limits),
		}
		status, ok := containerStatus(pod, container.Name)
		if ok {
			item.Ready = status.Ready
			item.Restarts = status.RestartCount
			item.State, item.Reason = containerState(status)
			item.OOMKilled = containerOOMKilled(status)
		} else {
			item.State = "unknown"
		}
		out.Containers = append(out.Containers, item)
		addResources(&out.Requests, item.Requests)
		addResources(&out.Limits, item.Limits)
	}

	if p == nil || p.client == nil {
		out.Reason = Reason(ErrNotConfigured)
		return out
	}
	metrics, err := p.podMetrics(ctx, pod.Name)
	if err != nil {
		out.Reason = Reason(err)
		return out
	}

	usageByName := make(map[string]metricsv1beta1.ContainerMetrics, len(metrics.Containers))
	for _, container := range metrics.Containers {
		usageByName[container.Name] = container
	}
	for i := range out.Containers {
		container, ok := usageByName[out.Containers[i].Name]
		if !ok {
			continue
		}
		usage := resourcesFromList(container.Usage)
		out.Containers[i].Usage = usage
		addResources(&out.Usage, usage)
	}

	out.Available = true
	out.Source = "metrics-server"
	if !metrics.Timestamp.IsZero() {
		observed := metrics.Timestamp.Time
		out.ObservedAt = &observed
	}
	if metrics.Window.Duration > 0 {
		out.WindowSeconds = int64(metrics.Window.Duration.Seconds())
	}
	return out
}

// Usage returns the live total usage for one pod.
func (p *Provider) Usage(ctx context.Context, podName string) (UsageSnapshot, error) {
	if p == nil || p.client == nil {
		return UsageSnapshot{}, ErrNotConfigured
	}
	metrics, err := p.podMetrics(ctx, podName)
	if err != nil {
		return UsageSnapshot{}, err
	}
	var out UsageSnapshot
	for _, container := range metrics.Containers {
		usage := resourcesFromList(container.Usage)
		if usage == nil {
			continue
		}
		if usage.CPU != nil {
			if out.CPU == nil {
				zero := 0.0
				out.CPU = &zero
			}
			*out.CPU += *usage.CPU
		}
		if usage.Memory != nil {
			if out.Memory == nil {
				zero := int64(0)
				out.Memory = &zero
			}
			*out.Memory += *usage.Memory
		}
	}
	if !metrics.Timestamp.IsZero() {
		observed := metrics.Timestamp.Time
		out.ObservedAt = &observed
	}
	if metrics.Window.Duration > 0 {
		out.WindowSeconds = int64(metrics.Window.Duration.Seconds())
	}
	return out, nil
}

func addResources(dst *ResourceUsage, src *ResourceUsage) {
	if src == nil {
		return
	}
	if src.CPU != nil {
		if dst.CPU == nil {
			zero := 0.0
			dst.CPU = &zero
		}
		*dst.CPU += *src.CPU
	}
	if src.Memory != nil {
		if dst.Memory == nil {
			zero := int64(0)
			dst.Memory = &zero
		}
		*dst.Memory += *src.Memory
	}
}

func resourcesFromList(list corev1.ResourceList) *ResourceUsage {
	cpu, hasCPU := list[corev1.ResourceCPU]
	memory, hasMemory := list[corev1.ResourceMemory]
	if !hasCPU && !hasMemory {
		return nil
	}
	out := &ResourceUsage{}
	if hasCPU {
		value := cpu.AsApproximateFloat64()
		out.CPU = &value
	}
	if hasMemory {
		value := memory.Value()
		out.Memory = &value
	}
	return out
}

func containerStatus(pod *corev1.Pod, name string) (corev1.ContainerStatus, bool) {
	for _, status := range pod.Status.ContainerStatuses {
		if status.Name == name {
			return status, true
		}
	}
	return corev1.ContainerStatus{}, false
}

func containerState(status corev1.ContainerStatus) (state, reason string) {
	switch {
	case status.State.Running != nil:
		return "running", ""
	case status.State.Waiting != nil:
		return "waiting", status.State.Waiting.Reason
	case status.State.Terminated != nil:
		return "terminated", status.State.Terminated.Reason
	default:
		return "unknown", ""
	}
}

func containerOOMKilled(status corev1.ContainerStatus) bool {
	if status.State.Terminated != nil && status.State.Terminated.Reason == "OOMKilled" {
		return true
	}
	return status.LastTerminationState.Terminated != nil &&
		status.LastTerminationState.Terminated.Reason == "OOMKilled"
}

func (p *Provider) podMetrics(ctx context.Context, podName string) (metricsv1beta1.PodMetrics, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := p.now()
	if now.Before(p.cache.expiresAt) {
		if p.cache.err != nil {
			return metricsv1beta1.PodMetrics{}, p.cache.err
		}
		if metrics, ok := p.cache.items[podName]; ok {
			return metrics, nil
		}
		return metricsv1beta1.PodMetrics{}, ErrNotReady
	}

	list, err := p.client.MetricsV1beta1().PodMetricses(p.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		p.cache = metricsCache{expiresAt: now.Add(errorCacheTTL), err: err}
		return metricsv1beta1.PodMetrics{}, err
	}
	items := make(map[string]metricsv1beta1.PodMetrics, len(list.Items))
	for _, item := range list.Items {
		items[item.Name] = item
	}
	p.cache = metricsCache{items: items, expiresAt: now.Add(p.ttl)}
	if metrics, ok := items[podName]; ok {
		return metrics, nil
	}
	return metricsv1beta1.PodMetrics{}, ErrNotReady
}

// Reason maps Kubernetes and metrics-server errors to a safe user-facing
// reason. Raw cluster errors are not exposed by the API.
func Reason(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrNotConfigured):
		return "live metrics are disabled"
	case errors.Is(err, ErrNotReady):
		return "metrics are not available yet"
	case apierrors.IsForbidden(err):
		return "metrics access is forbidden"
	case apierrors.IsNotFound(err):
		return "metrics-server is not installed"
	case apierrors.IsServiceUnavailable(err), apierrors.IsTimeout(err), apierrors.IsTooManyRequests(err):
		return "metrics-server is unavailable"
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return "metrics request timed out"
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return "metrics-server is unreachable"
	}
	return "metrics are unavailable"
}
