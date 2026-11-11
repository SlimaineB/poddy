package k8s

import (
	"encoding/json"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func hasContainer(pod *corev1.Pod, name string) bool {
	return hasContainerSpec(pod.Spec, name)
}

func hasContainerSpec(spec corev1.PodSpec, name string) bool {
	for _, container := range spec.Containers {
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
	cpu, memory := resourceRequests(pod.Spec)
	return Workspace{
		Name:          pod.Labels[labelWorkspace],
		Template:      pod.Labels[labelTemplate],
		Phase:         phase,
		Image:         containerImage(pod, ContainerName),
		Owner:         Owner(pod),
		Persistent:    hasPVC(pod),
		BrowserVSCode: hasContainer(pod, BrowserVSCodeContainer),
		JupyterLab:    hasContainer(pod, JupyterLabContainer),
		CPU:           quantityString(cpu),
		Memory:        quantityString(memory),
		Storage:       storageFromPod(pod),
		CreatedAt:     pod.CreationTimestamp.Time,
		Schedule:      scheduleFromPod(pod),
	}
}

func scheduleFromPod(pod *corev1.Pod) *Schedule {
	state, ok := stateFromPodAnnotation(pod)
	if !ok {
		return nil
	}
	return state.Schedule
}

func workspaceFromState(state WorkspaceState, phase string) Workspace {
	cpu, memory := resourceRequests(state.Spec)
	return Workspace{
		Name:              state.Name,
		Template:          state.Template,
		Phase:             phase,
		Image:             state.Image,
		Owner:             state.Owner,
		Persistent:        state.Persistent,
		BrowserVSCode:     hasContainerSpec(state.Spec, BrowserVSCodeContainer),
		JupyterLab:        hasContainerSpec(state.Spec, JupyterLabContainer),
		CPU:               quantityString(cpu),
		Memory:            quantityString(memory),
		Storage:           state.Storage,
		CreatedAt:         state.CreatedAt,
		Schedule:          cloneSchedule(state.Schedule),
		StoppedBySchedule: state.StoppedBySchedule,
	}
}

func resourceRequests(spec corev1.PodSpec) (cpu, memory resource.Quantity) {
	for _, container := range spec.Containers {
		if quantity, ok := container.Resources.Requests[corev1.ResourceCPU]; ok {
			cpu.Add(quantity)
		}
		if quantity, ok := container.Resources.Requests[corev1.ResourceMemory]; ok {
			memory.Add(quantity)
		}
	}
	return cpu, memory
}

func quantityString(quantity resource.Quantity) string {
	if quantity.IsZero() {
		return ""
	}
	return quantity.String()
}

func storageFromPod(pod *corev1.Pod) string {
	raw := pod.Annotations[annotationState]
	if raw == "" {
		return ""
	}
	var state WorkspaceState
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		return ""
	}
	return state.Storage
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
