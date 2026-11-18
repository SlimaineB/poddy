package k8s

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const agentTokenSecretKey = "token"

// AgentTokenSecretName returns the Secret holding the workspace's agent
// bootstrap token. The token is injected with SecretKeyRef instead of being
// written into the pod spec.
func AgentTokenSecretName(workspace string) string {
	name := "pd-agent-" + workspace
	if len(name) <= 63 {
		return name
	}
	sum := sha256.Sum256([]byte(workspace))
	return "pd-agent-" + hex.EncodeToString(sum[:8])
}

func (m *Manager) ensureAgentTokenSecret(ctx context.Context, workspace, token string) error {
	if token == "" {
		return fmt.Errorf("agent token for workspace %q is empty", workspace)
	}
	name := AgentTokenSecretName(workspace)
	data := map[string][]byte{agentTokenSecretKey: []byte(token)}
	desired := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: m.Namespace,
			Labels:    labels(workspace),
		},
		Type: corev1.SecretTypeOpaque,
		Data: data,
	}

	existing, err := m.Client.CoreV1().Secrets(m.Namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		if _, err := m.Client.CoreV1().Secrets(m.Namespace).Create(ctx, desired, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("create agent token secret: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("read agent token secret: %w", err)
	}
	if bytes.Equal(existing.Data[agentTokenSecretKey], []byte(token)) {
		return nil
	}
	existing.Data = data
	existing.StringData = nil
	existing.Labels = labels(workspace)
	if _, err := m.Client.CoreV1().Secrets(m.Namespace).Update(ctx, existing, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("update agent token secret: %w", err)
	}
	return nil
}

func (m *Manager) deleteAgentTokenSecret(ctx context.Context, workspace string) error {
	err := m.Client.CoreV1().Secrets(m.Namespace).Delete(ctx, AgentTokenSecretName(workspace), metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("delete agent token secret: %w", err)
	}
	return nil
}
