package k8s

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/yaml"
)

const (
	// DefaultStateConfigMap is the legacy location of stopped workspace specs.
	// StatefulSets are now the source of truth; the ConfigMap is only read for
	// migration and for workspaces created by older releases.
	DefaultStateConfigMap = "poddy-state"
	stateDataKey          = "workspaces.yaml"
	// annotationState keeps the original rendered spec on legacy pods.
	annotationState = "poddy.io/state"
)

// WorkspaceState is everything needed to recreate a stopped workspace pod.
type WorkspaceState struct {
	Name              string            `json:"name" yaml:"name"`
	Template          string            `json:"template,omitempty" yaml:"template,omitempty"`
	TemplateVersion   string            `json:"templateVersion,omitempty" yaml:"templateVersion,omitempty"`
	Owner             string            `json:"owner,omitempty" yaml:"owner,omitempty"`
	Image             string            `json:"image,omitempty" yaml:"image,omitempty"`
	CreatedAt         time.Time         `json:"createdAt,omitempty" yaml:"createdAt,omitempty"`
	StoppedAt         time.Time         `json:"stoppedAt,omitempty" yaml:"stoppedAt,omitempty"`
	Persistent        bool              `json:"persistent,omitempty" yaml:"persistent,omitempty"`
	Storage           string            `json:"storage,omitempty" yaml:"storage,omitempty"`
	StorageClass      string            `json:"storageClass,omitempty" yaml:"storageClass,omitempty"`
	Parameters        map[string]string `json:"parameters,omitempty" yaml:"parameters,omitempty"`
	Apps              []string          `json:"apps,omitempty" yaml:"apps,omitempty"`
	Schedule          *Schedule         `json:"schedule,omitempty" yaml:"schedule,omitempty"`
	StoppedBySchedule bool              `json:"stoppedBySchedule,omitempty" yaml:"stoppedBySchedule,omitempty"`
	PortalURL         string            `json:"portalUrl,omitempty" yaml:"portalUrl,omitempty"`
	Labels            map[string]string `json:"labels,omitempty" yaml:"labels,omitempty"`
	Annotations       map[string]string `json:"annotations,omitempty" yaml:"annotations,omitempty"`
	Spec              corev1.PodSpec    `json:"spec" yaml:"spec"`
}

type stateDocument struct {
	Workspaces map[string]WorkspaceState `json:"workspaces" yaml:"workspaces"`
}

// StateStore reads and writes the legacy stopped workspace ConfigMap.
type StateStore struct {
	client    kubernetes.Interface
	namespace string
	name      string
	mu        sync.Mutex
}

func NewStateStore(client kubernetes.Interface, namespace, name string) *StateStore {
	if name == "" {
		name = DefaultStateConfigMap
	}
	return &StateStore{client: client, namespace: namespace, name: name}
}

// Load returns the stopped workspaces. A missing ConfigMap is not an error.
func (s *StateStore) Load(ctx context.Context) (map[string]WorkspaceState, error) {
	states := map[string]WorkspaceState{}
	cm, err := s.client.CoreV1().ConfigMaps(s.namespace).Get(ctx, s.name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return states, nil
	}
	if err != nil {
		return nil, err
	}
	doc, err := parseStateDocument(cm)
	if err != nil {
		return nil, err
	}
	for key, state := range doc.Workspaces {
		states[key] = state
	}
	return states, nil
}

// Get returns one stopped workspace.
func (s *StateStore) Get(ctx context.Context, name string) (WorkspaceState, bool, error) {
	states, err := s.Load(ctx)
	if err != nil {
		return WorkspaceState{}, false, err
	}
	state, ok := states[name]
	return state, ok, nil
}

// Update applies mutate to the latest version of the stopped workspace
// document. Concurrent updates to different workspaces are preserved.
func (s *StateStore) Update(ctx context.Context, mutate func(map[string]WorkspaceState) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		states, err := s.Load(ctx)
		if err != nil {
			return err
		}
		if err := mutate(states); err != nil {
			return err
		}
		return s.writeConfigMap(ctx, states)
	})
}

func (s *StateStore) writeConfigMap(ctx context.Context, states map[string]WorkspaceState) error {
	cm, err := s.client.CoreV1().ConfigMaps(s.namespace).Get(ctx, s.name, metav1.GetOptions{})
	notFound := apierrors.IsNotFound(err)
	if err != nil && !notFound {
		return err
	}
	if len(states) == 0 {
		if notFound {
			return nil
		}
		if err := s.client.CoreV1().ConfigMaps(s.namespace).Delete(ctx, s.name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
		return nil
	}
	raw, err := yaml.Marshal(stateDocument{Workspaces: states})
	if err != nil {
		return fmt.Errorf("marshal workspace state: %w", err)
	}
	if notFound {
		_, err = s.client.CoreV1().ConfigMaps(s.namespace).Create(ctx, &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: s.name},
			Data:       map[string]string{stateDataKey: string(raw)},
		}, metav1.CreateOptions{})
		if apierrors.IsAlreadyExists(err) {
			return apierrors.NewConflict(corev1.Resource("configmaps"), s.name, err)
		}
		return err
	}
	if cm.Data == nil {
		cm.Data = map[string]string{}
	}
	if cm.Data[stateDataKey] == string(raw) {
		return nil
	}
	cm.Data[stateDataKey] = string(raw)
	_, err = s.client.CoreV1().ConfigMaps(s.namespace).Update(ctx, cm, metav1.UpdateOptions{})
	return err
}

// Save replaces the stopped workspace document.
func (s *StateStore) Save(ctx context.Context, states map[string]WorkspaceState) error {
	if states == nil {
		states = map[string]WorkspaceState{}
	}
	return s.Update(ctx, func(target map[string]WorkspaceState) error {
		for key := range target {
			delete(target, key)
		}
		for key, state := range states {
			target[key] = state
		}
		return nil
	})
}

func parseStateDocument(cm *corev1.ConfigMap) (stateDocument, error) {
	var doc stateDocument
	if raw := strings.TrimSpace(cm.Data[stateDataKey]); raw != "" {
		if err := yaml.Unmarshal([]byte(raw), &doc); err != nil {
			return doc, fmt.Errorf("parse workspace state %s: %w", cm.Name, err)
		}
	}
	if doc.Workspaces == nil {
		doc.Workspaces = map[string]WorkspaceState{}
	}
	return doc, nil
}

func marshalState(state WorkspaceState) (string, error) {
	data, err := json.Marshal(state)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func copyStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
