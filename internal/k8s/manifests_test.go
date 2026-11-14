package k8s

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestWorkspaceManifestsRunningAndStopped(t *testing.T) {
	m := newTestManager()
	ctx := context.Background()

	if _, err := m.Create(ctx, CreateRequest{Name: "app"}); err != nil {
		t.Fatal(err)
	}

	manifests, err := m.Manifests(ctx, "app")
	if err != nil {
		t.Fatal(err)
	}
	if manifests.PodSource != "live" || manifests.Pod == nil || manifests.PVC == nil {
		t.Fatalf("manifests = %+v", manifests)
	}
	if manifests.Pod.Kind != "Pod" || manifests.Pod.Name != PodName("app") || manifests.Pod.Namespace != "poddy" {
		t.Fatalf("pod = %+v", manifests.Pod)
	}
	if manifests.Pod.Status.Phase != "" || manifests.Pod.Spec.NodeName != "" {
		t.Fatalf("runtime fields were not cleared: %+v", manifests.Pod)
	}
	if manifests.PVC.Kind != "PersistentVolumeClaim" || manifests.PVC.Name != "poddy-app" {
		t.Fatalf("pvc = %+v", manifests.PVC)
	}
	if manifests.Template != "base" || manifests.Parameters["storage"] != "5Gi" {
		t.Fatalf("template metadata = %+v", manifests)
	}

	pod, err := m.Get(ctx, "app")
	if err != nil {
		t.Fatal(err)
	}
	pod.Spec.Containers[0].Env = append(pod.Spec.Containers[0].Env, corev1.EnvVar{Name: "GITHUB_TOKEN", Value: "super-secret"})
	if pod.Annotations == nil {
		pod.Annotations = map[string]string{}
	}
	pod.Annotations[annotationExternalAuthEnv] = `["GITHUB_TOKEN"]`
	if _, err := m.Client.CoreV1().Pods(m.Namespace).Update(ctx, pod, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}

	manifests, err = m.Manifests(ctx, "app")
	if err != nil {
		t.Fatal(err)
	}
	if !containsString(manifests.RedactedEnv, "GITHUB_TOKEN") {
		t.Fatalf("redacted env = %+v", manifests.RedactedEnv)
	}
	for _, env := range manifests.Pod.Spec.Containers[0].Env {
		if env.Name == "GITHUB_TOKEN" && env.Value != "<redacted>" {
			t.Fatalf("token was not redacted: %+v", env)
		}
	}

	if _, err := m.Stop(ctx, "app"); err != nil {
		t.Fatal(err)
	}
	manifests, err = m.Manifests(ctx, "app")
	if err != nil {
		t.Fatal(err)
	}
	if manifests.PodSource != "stopped-state" || manifests.Pod == nil || manifests.PVC == nil {
		t.Fatalf("stopped manifests = %+v", manifests)
	}
	if manifests.Pod.Annotations[annotationState] != "" {
		t.Fatalf("internal state annotation leaked: %+v", manifests.Pod.Annotations)
	}
	for _, env := range manifests.Pod.Spec.Containers[0].Env {
		if env.Name == "GITHUB_TOKEN" {
			t.Fatalf("external auth env leaked from stopped state: %+v", env)
		}
	}
	if !strings.Contains(manifests.Pod.Spec.Containers[0].Image, "poddy-workspace-base") {
		t.Fatalf("image = %q", manifests.Pod.Spec.Containers[0].Image)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
