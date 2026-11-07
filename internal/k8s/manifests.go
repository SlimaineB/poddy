package k8s

import (
	"context"
	"encoding/json"
	"regexp"
	"sort"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// WorkspaceManifests is the rendered Pod and PVC definition for a workspace.
// It is intended for administrators and never includes secret values.
type WorkspaceManifests struct {
	Name            string
	Template        string
	TemplateVersion string
	Parameters      map[string]string
	PodSource       string
	Pod             *corev1.Pod
	PVC             *corev1.PersistentVolumeClaim
	RedactedEnv     []string
}

var sensitiveEnvName = regexp.MustCompile(`(?i)(token|secret|password|passwd|credential|private[_-]?key|api[_-]?key|access[_-]?key)`)

// Manifests returns the rendered Pod and PVC definitions for a running or
// stopped workspace. Stopped workspaces use the spec saved in state; the PVC
// is read from the cluster when it still exists.
func (m *Manager) Manifests(ctx context.Context, name string) (WorkspaceManifests, error) {
	if err := validateName(name); err != nil {
		return WorkspaceManifests{}, err
	}

	out := WorkspaceManifests{Name: name}
	pod, err := m.Get(ctx, name)
	if err == nil {
		out.PodSource = "live"
		out.Template = pod.Labels[labelTemplate]
		if state, ok := stateFromPodAnnotation(pod); ok {
			out.TemplateVersion = state.TemplateVersion
			out.Parameters = state.Parameters
			if out.Template == "" {
				out.Template = state.Template
			}
		}
		out.Pod, out.RedactedEnv = sanitizePodForManifest(pod)
	} else if apierrors.IsNotFound(err) {
		state, stateErr := m.State(ctx, name)
		if stateErr != nil {
			return WorkspaceManifests{}, stateErr
		}
		out.PodSource = "stopped-state"
		out.Template = state.Template
		out.TemplateVersion = state.TemplateVersion
		out.Parameters = state.Parameters
		out.Pod, out.RedactedEnv = sanitizePodForManifest(m.podFromState(state))
	} else {
		return WorkspaceManifests{}, err
	}

	pvc, err := m.Client.CoreV1().PersistentVolumeClaims(m.Namespace).Get(ctx, PodName(name), metav1.GetOptions{})
	if err == nil {
		out.PVC = sanitizePVCForManifest(pvc)
	} else if !apierrors.IsNotFound(err) {
		return WorkspaceManifests{}, err
	}
	return out, nil
}

func stateFromPodAnnotation(pod *corev1.Pod) (WorkspaceState, bool) {
	if pod == nil || pod.Annotations == nil {
		return WorkspaceState{}, false
	}
	raw := pod.Annotations[annotationState]
	if raw == "" {
		return WorkspaceState{}, false
	}
	var state WorkspaceState
	if err := json.Unmarshal([]byte(raw), &state); err != nil || state.Name == "" {
		return WorkspaceState{}, false
	}
	return state, true
}

func sanitizePodForManifest(pod *corev1.Pod) (*corev1.Pod, []string) {
	copy := pod.DeepCopy()
	externalNames := externalAuthEnvNames(copy)
	redacted := redactPodEnv(copy, externalNames)

	copy.TypeMeta = metav1.TypeMeta{APIVersion: "v1", Kind: "Pod"}
	copy.Status = corev1.PodStatus{}
	copy.Spec.NodeName = ""
	clearRuntimeMetadata(&copy.ObjectMeta)
	if copy.Annotations != nil {
		delete(copy.Annotations, annotationState)
		delete(copy.Annotations, annotationExternalAuthEnv)
		delete(copy.Annotations, "kubectl.kubernetes.io/last-applied-configuration")
		if len(copy.Annotations) == 0 {
			copy.Annotations = nil
		}
	}
	return copy, redacted
}

func sanitizePVCForManifest(pvc *corev1.PersistentVolumeClaim) *corev1.PersistentVolumeClaim {
	copy := pvc.DeepCopy()
	copy.TypeMeta = metav1.TypeMeta{APIVersion: "v1", Kind: "PersistentVolumeClaim"}
	copy.Status = corev1.PersistentVolumeClaimStatus{}
	clearRuntimeMetadata(&copy.ObjectMeta)
	if copy.Annotations != nil {
		delete(copy.Annotations, "kubectl.kubernetes.io/last-applied-configuration")
		if len(copy.Annotations) == 0 {
			copy.Annotations = nil
		}
	}
	return copy
}

func clearRuntimeMetadata(meta *metav1.ObjectMeta) {
	meta.ResourceVersion = ""
	meta.UID = ""
	meta.SelfLink = ""
	meta.Generation = 0
	meta.CreationTimestamp = metav1.Time{}
	meta.DeletionTimestamp = nil
	meta.DeletionGracePeriodSeconds = nil
	meta.ManagedFields = nil
}

func redactPodEnv(pod *corev1.Pod, externalNames []string) []string {
	names := map[string]bool{envWorkspaceToken: true}
	for _, name := range externalNames {
		names[name] = true
	}
	redacted := map[string]bool{}
	redact := func(containers []corev1.Container) {
		for i := range containers {
			for j := range containers[i].Env {
				env := &containers[i].Env[j]
				if env.Value == "" {
					continue
				}
				if names[env.Name] || sensitiveEnvName.MatchString(env.Name) {
					env.Value = "<redacted>"
					redacted[env.Name] = true
				}
			}
		}
	}
	redact(pod.Spec.InitContainers)
	redact(pod.Spec.Containers)

	out := make([]string, 0, len(redacted))
	for name := range redacted {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
