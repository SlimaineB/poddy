package k8s

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"poddy/internal/templates"
)

func (m *Manager) Stop(ctx context.Context, name string) (Workspace, error) {
	return m.stopWorkspace(ctx, name, false)
}

// StopScheduled stops a workspace and marks it so the scheduler can start it
// again at the next active window.
func (m *Manager) StopScheduled(ctx context.Context, name string) (Workspace, error) {
	return m.stopWorkspace(ctx, name, true)
}

func (m *Manager) stopWorkspace(ctx context.Context, name string, scheduled bool) (Workspace, error) {
	if err := validateName(name); err != nil {
		return Workspace{}, err
	}
	release, err := m.lockWorkspace(ctx, name)
	if err != nil {
		return Workspace{}, err
	}
	defer release()
	return m.stop(ctx, name, scheduled)
}

func (m *Manager) stop(ctx context.Context, name string, scheduled bool) (Workspace, error) {
	pod, err := m.Get(ctx, name)
	if apierrors.IsNotFound(err) {
		if _, stateErr := m.State(ctx, name); stateErr == nil {
			return Workspace{}, fmt.Errorf("%w: %s", ErrNotRunning, name)
		}
		return Workspace{}, err
	}
	if err != nil {
		return Workspace{}, err
	}

	state := m.stateForPod(ctx, pod)
	state.StoppedAt = time.Now().UTC()
	state.StoppedBySchedule = scheduled
	if err := m.stateStore().Update(ctx, func(states map[string]WorkspaceState) error {
		states[name] = state
		return nil
	}); err != nil {
		return Workspace{}, err
	}

	grace := int64(0)
	if err := m.Client.CoreV1().Pods(m.Namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{
		GracePeriodSeconds: &grace,
	}); err != nil && !apierrors.IsNotFound(err) {
		return Workspace{}, err
	}
	return workspaceFromState(state, "Stopped"), nil
}

// Start recreates a stopped workspace pod from its stored spec.
func (m *Manager) Start(ctx context.Context, name string) (Workspace, error) {
	if err := validateName(name); err != nil {
		return Workspace{}, err
	}
	release, err := m.lockWorkspace(ctx, name)
	if err != nil {
		return Workspace{}, err
	}
	defer release()
	return m.start(ctx, name)
}

func (m *Manager) start(ctx context.Context, name string) (Workspace, error) {
	if _, err := m.Get(ctx, name); err == nil {
		return Workspace{}, fmt.Errorf("%w: %s", ErrAlreadyRunning, name)
	} else if !apierrors.IsNotFound(err) {
		return Workspace{}, err
	}

	state, err := m.State(ctx, name)
	if err != nil {
		return Workspace{}, err
	}
	return m.startFromState(ctx, name, state)
}

// Restart deletes and recreates a workspace pod, keeping its volume.
func (m *Manager) Restart(ctx context.Context, name string) (Workspace, error) {
	if err := validateName(name); err != nil {
		return Workspace{}, err
	}
	release, err := m.lockWorkspace(ctx, name)
	if err != nil {
		return Workspace{}, err
	}
	defer release()
	return m.restart(ctx, name)
}

func (m *Manager) restart(ctx context.Context, name string) (Workspace, error) {
	pod, err := m.Get(ctx, name)
	if apierrors.IsNotFound(err) {
		return m.start(ctx, name)
	}
	if err != nil {
		return Workspace{}, err
	}

	state := m.stateForPod(ctx, pod)
	state.StoppedAt = time.Now().UTC()
	state.StoppedBySchedule = false
	if err := m.stateStore().Update(ctx, func(states map[string]WorkspaceState) error {
		states[name] = state
		return nil
	}); err != nil {
		return Workspace{}, err
	}

	grace := int64(0)
	if err := m.Client.CoreV1().Pods(m.Namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{
		GracePeriodSeconds: &grace,
	}); err != nil && !apierrors.IsNotFound(err) {
		return Workspace{}, err
	}
	if err := m.waitForPodDeleted(ctx, pod.Name, 20*time.Second); err != nil {
		return Workspace{}, err
	}
	return m.startFromState(ctx, name, state)
}

func (m *Manager) startFromState(ctx context.Context, name string, state WorkspaceState) (Workspace, error) {
	if state.Name == "" {
		state.Name = name
	}
	if state.Storage != "" {
		if _, err := m.Client.CoreV1().PersistentVolumeClaims(m.Namespace).Get(ctx, PodName(name), metav1.GetOptions{}); apierrors.IsNotFound(err) {
			if _, err := m.createPVC(ctx, name, state.Template, state.Storage, state.StorageClass); err != nil {
				return Workspace{}, err
			}
		} else if err != nil {
			return Workspace{}, err
		}
	}

	pod := m.podFromState(state)
	if err := m.applySSHKeys(ctx, pod, name, state.Owner); err != nil {
		return Workspace{}, fmt.Errorf("SSH keys: %w", err)
	}
	var requirements []templates.ExternalAuth
	if m.Templates != nil && state.Template != "" {
		if tmpl, tmplErr := m.Templates.Get(state.Template); tmplErr == nil {
			requirements = tmpl.ExternalAuth
		}
	}
	if err := m.applyExternalAuth(ctx, pod, state.Owner, state.Name, state.PortalURL, requirements); err != nil {
		return Workspace{}, err
	}
	created, err := m.Client.CoreV1().Pods(m.Namespace).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		return Workspace{}, err
	}

	if err := m.stateStore().Update(ctx, func(states map[string]WorkspaceState) error {
		delete(states, name)
		return nil
	}); err != nil {
		log.Printf("start %s: clear stopped state: %v", name, err)
	}
	return toWorkspace(created), nil
}

func (m *Manager) podFromState(state WorkspaceState) *corev1.Pod {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        PodName(state.Name),
			Namespace:   m.Namespace,
			Labels:      copyStringMap(state.Labels),
			Annotations: copyStringMap(state.Annotations),
		},
		Spec: *state.Spec.DeepCopy(),
	}
	pod.Spec.NodeName = ""
	if pod.Labels == nil {
		pod.Labels = map[string]string{}
	}
	for key, value := range labels(state.Name) {
		pod.Labels[key] = value
	}
	pod.Labels[labelTemplate] = state.Template
	if pod.Annotations == nil {
		pod.Annotations = map[string]string{}
	}
	delete(pod.Annotations, annotationState)
	if state.Owner != "" {
		pod.Annotations[annotationOwner] = state.Owner
	}
	keys := m.authorizedKeysFor(state.Owner)
	for i := range pod.Spec.Containers {
		if pod.Spec.Containers[i].Name == ContainerName && len(keys) > 0 {
			setEnv(&pod.Spec.Containers[i], "SSH_AUTHORIZED_KEY", strings.Join(keys, "\n"))
		}
	}
	if pod.Spec.RestartPolicy == "" {
		pod.Spec.RestartPolicy = corev1.RestartPolicyAlways
	}
	state.StoppedBySchedule = false
	if raw, err := marshalState(state); err == nil {
		pod.Annotations[annotationState] = raw
	}
	return pod
}

func (m *Manager) stateForPod(ctx context.Context, pod *corev1.Pod) WorkspaceState {
	if raw := pod.Annotations[annotationState]; raw != "" {
		var state WorkspaceState
		if err := json.Unmarshal([]byte(raw), &state); err == nil && state.Name != "" {
			return state
		}
	}

	state := WorkspaceState{
		Name:        pod.Labels[labelWorkspace],
		Template:    pod.Labels[labelTemplate],
		Owner:       Owner(pod),
		Image:       containerImage(pod, ContainerName),
		CreatedAt:   pod.CreationTimestamp.Time,
		PortalURL:   envValue(pod, ContainerName, envPortalURL),
		Labels:      copyStringMap(pod.Labels),
		Annotations: copyStringMap(pod.Annotations),
		Spec:        *pod.Spec.DeepCopy(),
	}
	delete(state.Annotations, annotationState)
	stripExternalAuthEnv(&state.Spec, externalAuthEnvNames(pod))
	state.Spec.NodeName = ""
	for _, volume := range pod.Spec.Volumes {
		if volume.PersistentVolumeClaim == nil {
			continue
		}
		state.Persistent = true
		pvc, err := m.Client.CoreV1().PersistentVolumeClaims(m.Namespace).Get(ctx, volume.PersistentVolumeClaim.ClaimName, metav1.GetOptions{})
		if err == nil {
			if quantity, ok := pvc.Spec.Resources.Requests[corev1.ResourceStorage]; ok {
				state.Storage = quantity.String()
			}
			if pvc.Spec.StorageClassName != nil {
				state.StorageClass = *pvc.Spec.StorageClassName
			}
		}
		break
	}
	return state
}

func (m *Manager) waitForPodDeleted(ctx context.Context, podName string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		_, err := m.Client.CoreV1().Pods(m.Namespace).Get(ctx, podName, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for %s to be deleted", podName)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}
