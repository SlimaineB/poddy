package monitoring

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	metricsv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"
	metricsfake "k8s.io/metrics/pkg/client/clientset/versioned/fake"
)

func metricsClientWith(objects ...*metricsv1beta1.PodMetrics) *metricsfake.Clientset {
	client := metricsfake.NewSimpleClientset()
	for _, object := range objects {
		namespace := object.Namespace
		if namespace == "" {
			namespace = "poddy"
		}
		if err := client.Tracker().Create(metricsv1beta1.SchemeGroupVersion.WithResource("pods"), object, namespace); err != nil {
			panic(err)
		}
	}
	return client
}

func testPod() *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "poddy-app-0", Namespace: "poddy"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name: "workspace",
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("500m"),
						corev1.ResourceMemory: resource.MustParse("256Mi"),
					},
					Limits: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("2"),
						corev1.ResourceMemory: resource.MustParse("1Gi"),
					},
				},
			}},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{{
				Name:         "workspace",
				Ready:        true,
				RestartCount: 3,
				State:        corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
			}},
		},
	}
}

func TestProviderWorkspaceReturnsLiveUsage(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	metrics := &metricsv1beta1.PodMetrics{
		ObjectMeta: metav1.ObjectMeta{Name: "poddy-app-0", Namespace: "poddy"},
		Timestamp:  metav1.NewTime(now),
		Window:     metav1.Duration{Duration: 30 * time.Second},
		Containers: []metricsv1beta1.ContainerMetrics{{
			Name: "workspace",
			Usage: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("250m"),
				corev1.ResourceMemory: resource.MustParse("128Mi"),
			},
		}},
	}
	provider := NewProviderWithClient(metricsClientWith(metrics), "poddy", time.Minute)

	got := provider.Workspace(context.Background(), testPod())
	if !got.Available {
		t.Fatalf("available = false, reason = %q", got.Reason)
	}
	if got.Usage.CPU == nil || *got.Usage.CPU != 0.25 {
		t.Fatalf("cpu usage = %v", got.Usage.CPU)
	}
	if got.Usage.Memory == nil || *got.Usage.Memory != 128*1024*1024 {
		t.Fatalf("memory usage = %v", got.Usage.Memory)
	}
	if got.Requests.CPU == nil || *got.Requests.CPU != 0.5 {
		t.Fatalf("cpu request = %v", got.Requests.CPU)
	}
	if got.Limits.Memory == nil || *got.Limits.Memory != 1024*1024*1024 {
		t.Fatalf("memory limit = %v", got.Limits.Memory)
	}
	if len(got.Containers) != 1 || got.Containers[0].Restarts != 3 || !got.Containers[0].Ready {
		t.Fatalf("containers = %+v", got.Containers)
	}
	if got.ObservedAt == nil || !got.ObservedAt.Equal(now) {
		t.Fatalf("observedAt = %v", got.ObservedAt)
	}
	if got.WindowSeconds != 30 {
		t.Fatalf("window = %d", got.WindowSeconds)
	}
}

func TestProviderWorkspaceWithoutSampleKeepsDeclaredResources(t *testing.T) {
	provider := NewProviderWithClient(metricsClientWith(), "poddy", time.Minute)

	got := provider.Workspace(context.Background(), testPod())
	if got.Available {
		t.Fatal("available = true without a metrics sample")
	}
	if got.Reason != "metrics are not available yet" {
		t.Fatalf("reason = %q", got.Reason)
	}
	if got.Usage.CPU != nil || got.Usage.Memory != nil {
		t.Fatalf("usage = %+v, want nil", got.Usage)
	}
	if got.Requests.CPU == nil || *got.Requests.CPU != 0.5 {
		t.Fatalf("requests = %+v", got.Requests)
	}
}

func TestProviderWorkspaceNilPod(t *testing.T) {
	provider := NewProviderWithClient(metricsClientWith(), "poddy", time.Minute)
	got := provider.Workspace(context.Background(), nil)
	if got.Available || got.Reason != "workspace is not running" {
		t.Fatalf("got = %+v", got)
	}
}

func TestProviderCachesMetricsList(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	metrics := &metricsv1beta1.PodMetrics{
		ObjectMeta: metav1.ObjectMeta{Name: "poddy-app-0", Namespace: "poddy"},
		Timestamp:  metav1.NewTime(now),
		Containers: []metricsv1beta1.ContainerMetrics{{
			Name:  "workspace",
			Usage: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m")},
		}},
	}
	client := metricsClientWith(metrics)
	provider := NewProviderWithClient(client, "poddy", time.Minute)
	provider.now = func() time.Time { return now }

	provider.Workspace(context.Background(), testPod())
	provider.Workspace(context.Background(), testPod())
	lists := 0
	for _, action := range client.Actions() {
		if action.GetVerb() == "list" {
			lists++
		}
	}
	if lists != 1 {
		t.Fatalf("metrics-server list calls = %d, want 1", lists)
	}
}
