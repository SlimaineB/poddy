package k8s

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	sshKeysVolumeName = "poddy-ssh-keys"
	sshKeysMountPath  = "/etc/poddy/ssh"
	sshKeysDataKey    = "authorized_keys"
)

func sshKeysSecretName(workspace string) string {
	return sshKeysName(workspace)
}

// SyncSSHKeysForOwner refreshes the per-workspace authorized_keys Secret for
// every workspace owned by owner. Running pods pick up the update through the
// mounted Secret without a restart.
func (m *Manager) SyncSSHKeysForOwner(ctx context.Context, owner string) error {
	if owner == "" {
		return nil
	}
	workspaces, err := m.ListByOwner(ctx, owner)
	if err != nil {
		return err
	}
	for _, workspace := range workspaces {
		if err := m.ensureSSHKeysSecret(ctx, workspace.Name, workspace.Owner); err != nil {
			return fmt.Errorf("workspace %s: %w", workspace.Name, err)
		}
	}
	return nil
}

func (m *Manager) applySSHKeys(ctx context.Context, pod *corev1.Pod, workspace, owner string) error {
	if err := m.ensureSSHKeysSecret(ctx, workspace, owner); err != nil {
		return err
	}
	m.mountSSHKeys(pod, workspace)
	return nil
}

func (m *Manager) ensureSSHKeysSecret(ctx context.Context, workspace, owner string) error {
	name := sshKeysSecretName(workspace)
	keys := m.authorizedKeysFor(owner)
	data := []byte(strings.Join(keys, "\n"))
	if len(data) > 0 {
		data = append(data, '\n')
	}

	desired := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: m.Namespace,
			Labels:    labels(workspace),
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{sshKeysDataKey: data},
	}

	existing, err := m.Client.CoreV1().Secrets(m.Namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = m.Client.CoreV1().Secrets(m.Namespace).Create(ctx, desired, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}
	if bytes.Equal(existing.Data[sshKeysDataKey], data) {
		return nil
	}
	existing.Data = desired.Data
	_, err = m.Client.CoreV1().Secrets(m.Namespace).Update(ctx, existing, metav1.UpdateOptions{})
	return err
}

func (m *Manager) mountSSHKeys(pod *corev1.Pod, workspace string) {
	name := sshKeysSecretName(workspace)
	found := false
	for _, volume := range pod.Spec.Volumes {
		if volume.Name == sshKeysVolumeName && volume.Secret != nil && volume.Secret.SecretName == name {
			found = true
			break
		}
	}
	if !found {
		mode := int32(0o444)
		pod.Spec.Volumes = append(pod.Spec.Volumes, corev1.Volume{
			Name: sshKeysVolumeName,
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName:  name,
					DefaultMode: &mode,
				},
			},
		})
	}

	container := containerByName(pod, ContainerName)
	if container == nil {
		return
	}
	for _, mount := range container.VolumeMounts {
		if mount.Name == sshKeysVolumeName {
			return
		}
	}
	container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
		Name:      sshKeysVolumeName,
		MountPath: sshKeysMountPath,
		ReadOnly:  true,
	})
}

func (m *Manager) deleteSSHKeysSecret(ctx context.Context, workspace string) error {
	err := m.Client.CoreV1().Secrets(m.Namespace).Delete(ctx, sshKeysSecretName(workspace), metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}
