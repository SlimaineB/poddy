package k8s

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/utils/ptr"

	"poddy/internal/backup"
)

const (
	backupAppName      = "poddy-backup"
	backupContainer    = "helper"
	defaultHelperImage = "busybox:1.36"
	backupMountPath    = "/data"
)

func backupPodName(volume string) string {
	sum := sha256.Sum256([]byte(volume))
	return "poddy-bkp-" + hex.EncodeToString(sum[:])[:32]
}

func (m *Manager) helperImage() string {
	if image := strings.TrimSpace(m.HelperImage); image != "" {
		return image
	}
	return defaultHelperImage
}

// CreateVolumeHelper starts a short-lived pod that mounts a volume so the
// portal can stream tar archives in and out of it. The pod name is derived from
// the volume name and acts as a cross-replica operation lock. When nodeName is
// set the helper is pinned to that node, which allows a read-only mount while a
// ReadWriteOnce volume is still attached to a running workspace.
func (m *Manager) CreateVolumeHelper(ctx context.Context, volume string, readOnly bool, nodeName string) (string, error) {
	name := backupPodName(volume)
	existing, err := m.Client.CoreV1().Pods(m.Namespace).Get(ctx, name, metav1.GetOptions{})
	if err == nil {
		if existing.DeletionTimestamp == nil && existing.Status.Phase != corev1.PodSucceeded && existing.Status.Phase != corev1.PodFailed {
			return "", ErrVolumeBusy
		}
		if err := m.DeleteVolumeHelper(ctx, name); err != nil {
			return "", err
		}
	} else if !apierrors.IsNotFound(err) {
		return "", err
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: m.Namespace,
			Labels: map[string]string{
				labelName:      backupAppName,
				labelManagedBy: AppName,
				labelVolume:    volume,
			},
		},
		Spec: corev1.PodSpec{
			NodeName:                     nodeName,
			RestartPolicy:                corev1.RestartPolicyNever,
			AutomountServiceAccountToken: ptr.To(false),
			SecurityContext: &corev1.PodSecurityContext{
				RunAsUser:  ptr.To[int64](0),
				RunAsGroup: ptr.To[int64](0),
			},
			Containers: []corev1.Container{{
				Name:            backupContainer,
				Image:           m.helperImage(),
				ImagePullPolicy: corev1.PullIfNotPresent,
				Command:         []string{"sleep", "3600"},
				SecurityContext: &corev1.SecurityContext{
					AllowPrivilegeEscalation: ptr.To(false),
					Capabilities: &corev1.Capabilities{
						Drop: []corev1.Capability{"ALL"},
						Add:  []corev1.Capability{"CHOWN", "DAC_OVERRIDE", "DAC_READ_SEARCH", "FOWNER"},
					},
				},
				VolumeMounts: []corev1.VolumeMount{{
					Name:      "data",
					MountPath: backupMountPath,
					ReadOnly:  readOnly,
				}},
			}},
			Volumes: []corev1.Volume{{
				Name: "data",
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
						ClaimName: volume,
						ReadOnly:  readOnly,
					},
				},
			}},
		},
	}

	if _, err := m.Client.CoreV1().Pods(m.Namespace).Create(ctx, pod, metav1.CreateOptions{}); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return "", ErrVolumeBusy
		}
		return "", err
	}
	if err := m.waitForHelper(ctx, name); err != nil {
		_ = m.DeleteVolumeHelper(context.Background(), name)
		return "", err
	}
	return name, nil
}

func (m *Manager) waitForHelper(ctx context.Context, name string) error {
	deadline := time.Now().Add(90 * time.Second)
	for {
		pod, err := m.Client.CoreV1().Pods(m.Namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		switch pod.Status.Phase {
		case corev1.PodRunning:
			for _, status := range pod.Status.ContainerStatuses {
				if status.Name == backupContainer && status.Ready {
					return nil
				}
			}
		case corev1.PodSucceeded, corev1.PodFailed:
			return fmt.Errorf("backup helper stopped before it was ready: %s", pod.Status.Message)
		}
		for _, status := range pod.Status.ContainerStatuses {
			if status.State.Waiting != nil && status.State.Waiting.Reason != "" {
				if status.State.Waiting.Reason == "ErrImagePull" || status.State.Waiting.Reason == "ImagePullBackOff" || status.State.Waiting.Reason == "InvalidImageName" {
					return fmt.Errorf("backup helper image %s: %s", m.helperImage(), status.State.Waiting.Message)
				}
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for backup helper pod")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// DeleteVolumeHelper removes a helper pod and waits until it is gone.
func (m *Manager) DeleteVolumeHelper(ctx context.Context, name string) error {
	grace := int64(0)
	err := m.Client.CoreV1().Pods(m.Namespace).Delete(ctx, name, metav1.DeleteOptions{GracePeriodSeconds: &grace})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		_, err := m.Client.CoreV1().Pods(m.Namespace).Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for backup helper pod to stop")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// ExecStreamPod runs a command in a pod with streaming stdin/stdout.
func (m *Manager) ExecStreamPod(ctx context.Context, podName, container string, command []string, stdin io.Reader, stdout, stderr io.Writer) error {
	request := m.Client.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(m.Namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: container,
			Command:   command,
			Stdin:     stdin != nil,
			Stdout:    stdout != nil,
			Stderr:    stderr != nil,
			TTY:       false,
		}, scheme.ParameterCodec)

	executor, err := remotecommand.NewSPDYExecutor(m.Rest, http.MethodPost, request.URL())
	if err != nil {
		return err
	}
	return executor.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdin:  stdin,
		Stdout: stdout,
		Stderr: stderr,
	})
}

// volumeNode returns the node running the workspace that owns a volume. Pinning
// the helper to the same node lets a ReadWriteOnce volume be mounted read-only
// while the workspace is still running.
func (m *Manager) volumeNode(ctx context.Context, volume string) string {
	pvc, err := m.Client.CoreV1().PersistentVolumeClaims(m.Namespace).Get(ctx, volume, metav1.GetOptions{})
	if err != nil {
		return ""
	}
	workspace := pvc.Labels[labelWorkspace]
	if workspace == "" {
		return ""
	}
	pod, err := m.Client.CoreV1().Pods(m.Namespace).Get(ctx, PodName(workspace), metav1.GetOptions{})
	if err != nil || pod.DeletionTimestamp != nil {
		return ""
	}
	return pod.Spec.NodeName
}

// BackupVolume streams a gzip tar archive of the volume to the backup store.
func (m *Manager) BackupVolume(ctx context.Context, volume, key string, store *backup.Store) error {
	if !store.Enabled() {
		return fmt.Errorf("backup storage is not configured")
	}
	pod, err := m.CreateVolumeHelper(ctx, volume, true, m.volumeNode(ctx, volume))
	if err != nil {
		return err
	}
	defer m.DeleteVolumeHelper(context.Background(), pod)

	reader, writer := io.Pipe()
	errCh := make(chan error, 1)
	var stderr bytes.Buffer
	go func() {
		err := m.ExecStreamPod(ctx, pod, backupContainer, []string{"sh", "-c", "tar -C " + backupMountPath + " -cf - . | gzip -c"}, nil, writer, &stderr)
		writer.CloseWithError(err)
		errCh <- err
	}()

	putErr := store.Put(ctx, key, reader, -1, "application/gzip")
	_ = reader.CloseWithError(putErr)
	execErr := <-errCh
	if execErr != nil {
		if message := strings.TrimSpace(stderr.String()); message != "" {
			return fmt.Errorf("backup archive: %s", message)
		}
		return fmt.Errorf("backup archive: %w", execErr)
	}
	if putErr != nil {
		return putErr
	}
	return nil
}

// RestoreVolume streams a gzip tar archive from the backup store into a volume.
// When clean is true, existing volume contents are removed first.
func (m *Manager) RestoreVolume(ctx context.Context, volume, key string, store *backup.Store, clean bool) error {
	if !store.Enabled() {
		return fmt.Errorf("backup storage is not configured")
	}
	reader, err := store.Get(ctx, key)
	if err != nil {
		return err
	}
	defer reader.Close()

	pod, err := m.CreateVolumeHelper(ctx, volume, false, "")
	if err != nil {
		return err
	}
	defer m.DeleteVolumeHelper(context.Background(), pod)

	var stderr bytes.Buffer
	if clean {
		if err := m.ExecStreamPod(ctx, pod, backupContainer, []string{"sh", "-c", "rm -rf " + backupMountPath + "/* " + backupMountPath + "/.[!.]* " + backupMountPath + "/..?* 2>/dev/null; true"}, nil, io.Discard, &stderr); err != nil {
			if message := strings.TrimSpace(stderr.String()); message != "" {
				return fmt.Errorf("clean volume: %s", message)
			}
			return fmt.Errorf("clean volume: %w", err)
		}
	}

	stderr.Reset()
	if err := m.ExecStreamPod(ctx, pod, backupContainer, []string{"sh", "-c", "gzip -dc | tar -C " + backupMountPath + " -xf -"}, reader, io.Discard, &stderr); err != nil {
		if message := strings.TrimSpace(stderr.String()); message != "" {
			return fmt.Errorf("restore archive: %s", message)
		}
		return fmt.Errorf("restore archive: %w", err)
	}
	return nil
}
