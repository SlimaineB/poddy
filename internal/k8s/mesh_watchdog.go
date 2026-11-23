package k8s

import (
	"context"
	"log"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

// MeshUnhealthyGrace is how long a workspace pod may stay unready after the
// mesh agent has restarted before the pod is recreated. A container restart
// keeps the pod network namespace, which can leave stale control-plane state;
// recreating the pod gives the agent a fresh network identity.
const MeshUnhealthyGrace = 2 * time.Minute

// RecreateUnhealthyMeshPods deletes workspace pods whose mesh agent has
// restarted but is still not ready. The StatefulSet controller then creates a
// fresh pod, which recovers from a control-plane restart without manual ops.
func (m *Manager) RecreateUnhealthyMeshPods(ctx context.Context, grace time.Duration) error {
	if grace <= 0 {
		grace = MeshUnhealthyGrace
	}
	pods, err := m.Client.CoreV1().Pods(m.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelName + "=" + AppName,
	})
	if err != nil {
		return err
	}
	now := time.Now()
	for i := range pods.Items {
		pod := &pods.Items[i]
		if pod.DeletionTimestamp != nil || pod.Labels[labelWorkspace] == "" {
			continue
		}
		status, ok := findContainerStatus(pod.Status.ContainerStatuses, AgentContainerName)
		if !ok || status.Ready || status.RestartCount == 0 {
			continue
		}
		unreadyFor := podUnreadyFor(pod, now)
		if unreadyFor < grace {
			continue
		}
		log.Printf("mesh watchdog: recreating workspace %q pod %s: agent unready for %s after %d restarts",
			pod.Labels[labelWorkspace], pod.Name, unreadyFor.Round(time.Second), status.RestartCount)
		if err := m.Client.CoreV1().Pods(m.Namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{
			GracePeriodSeconds: ptr.To[int64](0),
		}); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

// MeshPeerOnlineFunc reports whether the workspace's mesh peer is present and
// online in the tailnet. The portal passes its own mesh node for this check.
type MeshPeerOnlineFunc func(ctx context.Context, workspace string) (bool, error)

// RecreateOfflineMeshPods deletes workspace pods whose mesh peer has been
// offline for grace. A pod can stay Ready while its tailnet node is gone from
// the control plane; recreating the pod gives the agent a fresh identity.
func (m *Manager) RecreateOfflineMeshPods(ctx context.Context, grace time.Duration, peerOnline MeshPeerOnlineFunc, offlineSince map[string]time.Time) error {
	if peerOnline == nil {
		return nil
	}
	if grace <= 0 {
		grace = MeshUnhealthyGrace
	}
	if offlineSince == nil {
		offlineSince = map[string]time.Time{}
	}
	pods, err := m.Client.CoreV1().Pods(m.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelName + "=" + AppName,
	})
	if err != nil {
		return err
	}
	now := time.Now()
	for i := range pods.Items {
		pod := &pods.Items[i]
		workspace := pod.Labels[labelWorkspace]
		if pod.DeletionTimestamp != nil || workspace == "" {
			continue
		}
		online, err := peerOnline(ctx, workspace)
		if err != nil {
			log.Printf("mesh watchdog: workspace %q peer status: %v", workspace, err)
			continue
		}
		if online {
			delete(offlineSince, workspace)
			continue
		}
		since, ok := offlineSince[workspace]
		if !ok || since.IsZero() {
			offlineSince[workspace] = now
			continue
		}
		if now.Sub(since) < grace {
			continue
		}
		log.Printf("mesh watchdog: recreating workspace %q pod %s: mesh peer offline for %s",
			workspace, pod.Name, now.Sub(since).Round(time.Second))
		if err := m.Client.CoreV1().Pods(m.Namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{
			GracePeriodSeconds: ptr.To[int64](0),
		}); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
		delete(offlineSince, workspace)
	}
	return nil
}

func podUnreadyFor(pod *corev1.Pod, now time.Time) time.Duration {
	for _, condition := range pod.Status.Conditions {
		if condition.Type != corev1.PodReady || condition.Status != corev1.ConditionFalse {
			continue
		}
		if condition.LastTransitionTime.IsZero() {
			return 0
		}
		return now.Sub(condition.LastTransitionTime.Time)
	}
	return 0
}
