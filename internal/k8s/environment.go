package k8s

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EnvironmentFunc resolves the effective plain and secret environment for a
// workspace. Values keys are environment variable names; secrets marks the
// keys that must be injected from a Kubernetes Secret.
type EnvironmentFunc func(ctx context.Context, owner, template, workspace string) (map[string]string, map[string]bool, error)

// EnvironmentSecretName returns the Kubernetes Secret holding the workspace's
// secret environment values. The name fits the DNS label limit for the maximum
// workspace name length.
func EnvironmentSecretName(workspace string) string {
	name := "pd-env-" + workspace
	if len(name) <= 63 {
		return name
	}
	sum := sha256.Sum256([]byte(workspace))
	return "pd-env-" + hex.EncodeToString(sum[:8])
}

func (m *Manager) applyEnvironment(ctx context.Context, pod *corev1.Pod, owner, template, workspace string) error {
	if m.Environment == nil {
		return nil
	}
	values, secrets, err := m.Environment(ctx, owner, template, workspace)
	if err != nil {
		return fmt.Errorf("environment: %w", err)
	}
	return m.applyEnvironmentValues(ctx, pod, workspace, values, secrets)
}

func (m *Manager) applyEnvironmentValues(ctx context.Context, pod *corev1.Pod, workspace string, values map[string]string, secrets map[string]bool) error {
	container := containerByName(pod, ContainerName)
	if container == nil {
		return nil
	}
	secretName := EnvironmentSecretName(workspace)
	stripEnvironmentEnv(&pod.Spec, environmentEnvNames(pod), secretName)

	plainNames := make([]string, 0, len(values))
	secretValues := map[string]string{}
	for name, value := range values {
		if secrets[name] {
			secretValues[name] = value
			continue
		}
		setEnv(container, name, value)
		plainNames = append(plainNames, name)
	}
	sort.Strings(plainNames)

	if len(secretValues) > 0 {
		if err := m.upsertEnvironmentSecret(ctx, workspace, secretValues); err != nil {
			return err
		}
		addEnvFromSecret(container, secretName)
	} else {
		removeEnvFromSecret(container, secretName)
		if err := m.deleteEnvironmentSecret(ctx, workspace); err != nil {
			return err
		}
	}

	if pod.Annotations == nil {
		pod.Annotations = map[string]string{}
	}
	if len(plainNames) == 0 {
		delete(pod.Annotations, annotationEnvironmentEnv)
		return nil
	}
	raw, err := json.Marshal(plainNames)
	if err != nil {
		return err
	}
	pod.Annotations[annotationEnvironmentEnv] = string(raw)
	return nil
}

func (m *Manager) upsertEnvironmentSecret(ctx context.Context, workspace string, data map[string]string) error {
	values := make(map[string][]byte, len(data))
	total := 0
	for name, value := range data {
		total += len(name) + len(value)
		values[name] = []byte(value)
	}
	if total > 900*1024 {
		return fmt.Errorf("environment secrets for workspace %q exceed the 900 KiB Kubernetes Secret budget", workspace)
	}
	name := EnvironmentSecretName(workspace)
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: m.Namespace,
			Labels:    labels(workspace),
		},
		Type: corev1.SecretTypeOpaque,
		Data: values,
	}
	existing, err := m.Client.CoreV1().Secrets(m.Namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		if _, err := m.Client.CoreV1().Secrets(m.Namespace).Create(ctx, secret, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("create environment secret: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("read environment secret: %w", err)
	}
	existing.Data = values
	existing.StringData = nil
	existing.Labels = labels(workspace)
	if _, err := m.Client.CoreV1().Secrets(m.Namespace).Update(ctx, existing, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("update environment secret: %w", err)
	}
	return nil
}

func (m *Manager) deleteEnvironmentSecret(ctx context.Context, workspace string) error {
	err := m.Client.CoreV1().Secrets(m.Namespace).Delete(ctx, EnvironmentSecretName(workspace), metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("delete environment secret: %w", err)
	}
	return nil
}

func environmentEnvNames(pod *corev1.Pod) []string {
	if pod == nil {
		return nil
	}
	return environmentEnvNamesFromAnnotations(pod.Annotations)
}

func environmentEnvNamesFromAnnotations(annotations map[string]string) []string {
	if annotations == nil {
		return nil
	}
	var names []string
	if raw := annotations[annotationEnvironmentEnv]; raw != "" {
		_ = json.Unmarshal([]byte(raw), &names)
	}
	return names
}

func stripEnvironmentEnv(spec *corev1.PodSpec, names []string, secretName string) {
	remove := make(map[string]bool, len(names))
	for _, name := range names {
		remove[name] = true
	}
	for i := range spec.Containers {
		container := &spec.Containers[i]
		if len(remove) > 0 {
			kept := container.Env[:0]
			for _, item := range container.Env {
				if !remove[item.Name] {
					kept = append(kept, item)
				}
			}
			container.Env = kept
		}
		removeEnvFromSecret(container, secretName)
	}
}

func addEnvFromSecret(container *corev1.Container, name string) {
	for _, source := range container.EnvFrom {
		if source.SecretRef != nil && source.SecretRef.Name == name {
			return
		}
	}
	container.EnvFrom = append(container.EnvFrom, corev1.EnvFromSource{
		SecretRef: &corev1.SecretEnvSource{
			LocalObjectReference: corev1.LocalObjectReference{Name: name},
		},
	})
}

func removeEnvFromSecret(container *corev1.Container, name string) {
	if len(container.EnvFrom) == 0 {
		return
	}
	kept := container.EnvFrom[:0]
	for _, source := range container.EnvFrom {
		if source.SecretRef != nil && source.SecretRef.Name == name {
			continue
		}
		kept = append(kept, source)
	}
	container.EnvFrom = kept
}
