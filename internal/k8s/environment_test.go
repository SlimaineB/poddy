package k8s

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestApplyEnvironmentCreatesAndCleansSecret(t *testing.T) {
	m := newTestManager()
	ctx := context.Background()
	if _, err := m.Create(ctx, CreateRequest{Name: "envtest"}); err != nil {
		t.Fatal(err)
	}
	pod, err := m.Get(ctx, "envtest")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.applyEnvironmentValues(ctx, pod, "envtest", map[string]string{
		"PLAIN":  "visible",
		"SECRET": "hidden",
	}, map[string]bool{"SECRET": true}); err != nil {
		t.Fatal(err)
	}
	if got := envValue(pod, ContainerName, "PLAIN"); got != "visible" {
		t.Fatalf("PLAIN = %q", got)
	}
	if got := envValue(pod, ContainerName, "SECRET"); got != "" {
		t.Fatalf("SECRET should not be a direct env var: %q", got)
	}
	container := containerByName(pod, ContainerName)
	found := false
	for _, source := range container.EnvFrom {
		if source.SecretRef != nil && source.SecretRef.Name == EnvironmentSecretName("envtest") {
			found = true
		}
	}
	if !found {
		t.Fatalf("envFrom = %+v", container.EnvFrom)
	}
	secret, err := m.Client.CoreV1().Secrets(m.Namespace).Get(ctx, EnvironmentSecretName("envtest"), metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if string(secret.Data["SECRET"]) != "hidden" {
		t.Fatalf("secret = %#v", secret.Data)
	}

	if err := m.applyEnvironmentValues(ctx, pod, "envtest", nil, nil); err != nil {
		t.Fatal(err)
	}
	if got := envValue(pod, ContainerName, "PLAIN"); got != "" {
		t.Fatalf("PLAIN was not removed: %q", got)
	}
	container = containerByName(pod, ContainerName)
	for _, source := range container.EnvFrom {
		if source.SecretRef != nil && source.SecretRef.Name == EnvironmentSecretName("envtest") {
			t.Fatalf("envFrom was not removed: %+v", container.EnvFrom)
		}
	}
	if _, err := m.Client.CoreV1().Secrets(m.Namespace).Get(ctx, EnvironmentSecretName("envtest"), metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("secret still exists: %v", err)
	}
}

func TestEnvironmentSecretNameFitsDNSLabel(t *testing.T) {
	long := strings.Repeat("a", 63)
	name := EnvironmentSecretName(long)
	if len(name) > 63 {
		t.Fatalf("secret name too long: %d", len(name))
	}
	if name == EnvironmentSecretName(long+"b") {
		t.Fatal("long workspace names should not collide")
	}
}

func TestWorkspaceEnvironmentLifecycle(t *testing.T) {
	m := newTestManager()
	m.Environment = func(context.Context, string, string, string) (map[string]string, map[string]bool, error) {
		return map[string]string{"PLAIN": "visible", "SECRET": "hidden"}, map[string]bool{"SECRET": true}, nil
	}
	ctx := context.Background()
	if _, err := m.Create(ctx, CreateRequest{Name: "envlife", Owner: "alice@example.com"}); err != nil {
		t.Fatal(err)
	}
	pod, err := m.Get(ctx, "envlife")
	if err != nil {
		t.Fatal(err)
	}
	if got := envValue(pod, ContainerName, "PLAIN"); got != "visible" {
		t.Fatalf("PLAIN = %q", got)
	}
	if _, err := m.Client.CoreV1().Secrets(m.Namespace).Get(ctx, EnvironmentSecretName("envlife"), metav1.GetOptions{}); err != nil {
		t.Fatal(err)
	}

	if _, err := m.Stop(ctx, "envlife"); err != nil {
		t.Fatal(err)
	}
	sts, err := m.GetStatefulSet(ctx, "envlife")
	if err != nil {
		t.Fatal(err)
	}
	container := containerByName(&corev1.Pod{Spec: sts.Spec.Template.Spec}, ContainerName)
	if got := envValue(&corev1.Pod{Spec: sts.Spec.Template.Spec}, ContainerName, "PLAIN"); got != "" {
		t.Fatalf("PLAIN survived stop: %q", got)
	}
	for _, source := range container.EnvFrom {
		if source.SecretRef != nil && source.SecretRef.Name == EnvironmentSecretName("envlife") {
			t.Fatalf("envFrom survived stop: %+v", container.EnvFrom)
		}
	}

	if _, err := m.Start(ctx, "envlife"); err != nil {
		t.Fatal(err)
	}
	pod, err = m.Get(ctx, "envlife")
	if err != nil {
		t.Fatal(err)
	}
	if got := envValue(pod, ContainerName, "PLAIN"); got != "visible" {
		t.Fatalf("PLAIN after start = %q", got)
	}

	if err := m.Delete(ctx, "envlife"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Client.CoreV1().Secrets(m.Namespace).Get(ctx, EnvironmentSecretName("envlife"), metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("secret still exists: %v", err)
	}
}
