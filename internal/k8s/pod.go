package k8s

import (
	corev1 "k8s.io/api/core/v1"
)

func hasContainer(pod *corev1.Pod, name string) bool {
	for _, container := range pod.Spec.Containers {
		if container.Name == name {
			return true
		}
	}
	return false
}

func toWorkspace(pod *corev1.Pod) Workspace {
	phase := string(pod.Status.Phase)
	if pod.DeletionTimestamp != nil {
		phase = "Terminating"
	}
	return Workspace{
		Name:       pod.Labels[labelWorkspace],
		Template:   pod.Labels[labelTemplate],
		Phase:      phase,
		Image:      containerImage(pod, ContainerName),
		Owner:      Owner(pod),
		Persistent: hasPVC(pod),
		CreatedAt:  pod.CreationTimestamp.Time,
	}
}

func workspaceFromState(state WorkspaceState, phase string) Workspace {
	return Workspace{
		Name:       state.Name,
		Template:   state.Template,
		Phase:      phase,
		Image:      state.Image,
		Owner:      state.Owner,
		Persistent: state.Persistent,
		CreatedAt:  state.CreatedAt,
	}
}

func containerImage(pod *corev1.Pod, name string) string {
	for _, container := range pod.Spec.Containers {
		if container.Name == name {
			return container.Image
		}
	}
	return ""
}

func hasPVC(pod *corev1.Pod) bool {
	for _, volume := range pod.Spec.Volumes {
		if volume.PersistentVolumeClaim != nil {
			return true
		}
	}
	return false
}
