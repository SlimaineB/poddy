package k8s

import (
	"context"
	"encoding/json"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

// WorkspaceSnapshot is the metadata mirror representation of a workspace. It
// never contains external auth tokens or PVC contents.
type WorkspaceSnapshot struct {
	Name            string
	Template        string
	TemplateVersion string
	Owner           string
	Image           string
	Persistent      bool
	Storage         string
	StorageClass    string
	Parameters      map[string]string
	Labels          map[string]string
	Annotations     map[string]string
	RenderedPod     []byte
	ExternalAuthEnv []string
	PortalURL       string
	Phase           string
	DesiredState    string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	StoppedAt       time.Time
}

// Snapshot returns the current state of a running or stopped workspace.
func (m *Manager) Snapshot(ctx context.Context, name string) (WorkspaceSnapshot, error) {
	pod, err := m.Get(ctx, name)
	if err == nil {
		phase := string(pod.Status.Phase)
		if pod.DeletionTimestamp != nil {
			phase = "Terminating"
			if state, stateErr := m.State(ctx, name); stateErr == nil && !state.StoppedAt.IsZero() {
				return snapshotFromState(state, phase, "stopped", state.StoppedAt)
			}
		}
		state := m.stateForPod(ctx, pod)
		return snapshotFromState(state, phase, "running", time.Now().UTC())
	}
	if !apierrors.IsNotFound(err) {
		return WorkspaceSnapshot{}, err
	}

	state, err := m.State(ctx, name)
	if err != nil {
		return WorkspaceSnapshot{}, err
	}
	return snapshotFromState(state, "Stopped", "stopped", state.StoppedAt)
}

func snapshotFromState(state WorkspaceState, phase, desired string, updatedAt time.Time) (WorkspaceSnapshot, error) {
	if state.Name == "" {
		return WorkspaceSnapshot{}, nil
	}
	externalAuthEnv := externalAuthEnvNamesFromAnnotations(state.Annotations)
	spec := *state.Spec.DeepCopy()
	stripExternalAuthEnv(&spec, externalAuthEnv)
	rendered, err := json.Marshal(spec)
	if err != nil {
		return WorkspaceSnapshot{}, err
	}
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}
	return WorkspaceSnapshot{
		Name:            state.Name,
		Template:        state.Template,
		TemplateVersion: state.TemplateVersion,
		Owner:           state.Owner,
		Image:           state.Image,
		Persistent:      state.Persistent,
		Storage:         state.Storage,
		StorageClass:    state.StorageClass,
		Parameters:      state.Parameters,
		Labels:          state.Labels,
		Annotations:     state.Annotations,
		RenderedPod:     rendered,
		ExternalAuthEnv: externalAuthEnv,
		PortalURL:       state.PortalURL,
		Phase:           phase,
		DesiredState:    desired,
		CreatedAt:       state.CreatedAt,
		UpdatedAt:       updatedAt,
		StoppedAt:       state.StoppedAt,
	}, nil
}

func externalAuthEnvNamesFromAnnotations(annotations map[string]string) []string {
	if annotations == nil {
		return nil
	}
	var names []string
	if raw := annotations[annotationExternalAuthEnv]; raw != "" {
		_ = json.Unmarshal([]byte(raw), &names)
	}
	return names
}
