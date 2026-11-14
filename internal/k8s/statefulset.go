package k8s

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

const (
	// GoverningService is the headless Service used by workspace StatefulSets
	// for stable network identity.
	GoverningService = "poddy-workspaces"

	annotationSchedule          = "poddy.io/schedule"
	annotationStoppedAt         = "poddy.io/stopped-at"
	annotationStoppedBySchedule = "poddy.io/stopped-by-schedule"
	annotationTemplateVersion   = "poddy.io/template-version"
	annotationParameters        = "poddy.io/parameters"
	annotationStorage           = "poddy.io/storage"
	annotationStorageClass      = "poddy.io/storage-class"
	annotationPortalURL         = "poddy.io/portal-url"
)

func StatefulSetName(name string) string {
	return "poddy-" + name
}

func PVCName(name string) string {
	return "poddy-" + name
}

func sshKeysName(name string) string {
	return StatefulSetName(name) + "-ssh-keys"
}

func workspaceName(sts *appsv1.StatefulSet) string {
	if sts == nil {
		return ""
	}
	if name := sts.Labels[labelWorkspace]; name != "" {
		return name
	}
	if name := sts.Spec.Template.Labels[labelWorkspace]; name != "" {
		return name
	}
	return strings.TrimPrefix(sts.Name, "poddy-")
}

func (m *Manager) buildStatefulSet(workspace string, pod *corev1.Pod, replicas int32) *appsv1.StatefulSet {
	labels := copyStringMap(pod.Labels)
	if labels == nil {
		labels = map[string]string{}
	}
	for key, value := range pod.Labels {
		labels[key] = value
	}
	for key, value := range labelsFor(workspace) {
		labels[key] = value
	}
	annotations := copyStringMap(pod.Annotations)
	if annotations == nil {
		annotations = map[string]string{}
	}
	delete(annotations, annotationState)

	spec := *pod.Spec.DeepCopy()
	spec.NodeName = ""
	spec.RestartPolicy = corev1.RestartPolicyAlways

	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:        StatefulSetName(workspace),
			Namespace:   m.Namespace,
			Labels:      labels,
			Annotations: copyStringMap(annotations),
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas:            ptr.To(replicas),
			ServiceName:         GoverningService,
			PodManagementPolicy: appsv1.ParallelPodManagement,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					labelName:      AppName,
					labelWorkspace: workspace,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      labels,
					Annotations: copyStringMap(annotations),
				},
				Spec: spec,
			},
			UpdateStrategy: appsv1.StatefulSetUpdateStrategy{
				Type: appsv1.OnDeleteStatefulSetStrategyType,
			},
			RevisionHistoryLimit: ptr.To(int32(0)),
		},
	}
}

func labelsFor(workspace string, extra ...map[string]string) map[string]string {
	out := labels(workspace)
	for _, set := range extra {
		for key, value := range set {
			out[key] = value
		}
	}
	return out
}

func podFromStatefulSet(sts *appsv1.StatefulSet) *corev1.Pod {
	name := workspaceName(sts)
	labels := copyStringMap(sts.Spec.Template.Labels)
	if labels == nil {
		labels = map[string]string{}
	}
	for key, value := range labelsFor(name) {
		labels[key] = value
	}
	annotations := copyStringMap(sts.Spec.Template.Annotations)
	if annotations == nil {
		annotations = map[string]string{}
	}
	for key, value := range sts.Annotations {
		if _, ok := annotations[key]; !ok {
			annotations[key] = value
		}
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              PodName(name),
			Namespace:         sts.Namespace,
			Labels:            labels,
			Annotations:       annotations,
			CreationTimestamp: sts.CreationTimestamp,
		},
		Spec: *sts.Spec.Template.Spec.DeepCopy(),
	}
	pod.Spec.NodeName = ""
	return pod
}

func workspaceFromStatefulSet(sts *appsv1.StatefulSet) Workspace {
	pod := podFromStatefulSet(sts)
	workspace := toWorkspace(pod)
	workspace.Name = workspaceName(sts)
	workspace.CreatedAt = sts.CreationTimestamp.Time
	workspace.StoppedBySchedule = sts.Annotations[annotationStoppedBySchedule] == "true"
	return workspace
}

func phaseForStatefulSet(sts *appsv1.StatefulSet, pod *corev1.Pod) string {
	if pod != nil {
		if pod.DeletionTimestamp != nil {
			return "Terminating"
		}
		return string(pod.Status.Phase)
	}
	if sts.Spec.Replicas == nil || *sts.Spec.Replicas == 0 {
		return "Stopped"
	}
	return "Pending"
}

func stateFromStatefulSet(sts *appsv1.StatefulSet) WorkspaceState {
	pod := podFromStatefulSet(sts)
	name := workspaceName(sts)
	state := WorkspaceState{
		Name:            name,
		Template:        pod.Labels[labelTemplate],
		TemplateVersion: pod.Annotations[annotationTemplateVersion],
		Owner:           Owner(pod),
		Image:           containerImage(pod, ContainerName),
		CreatedAt:       sts.CreationTimestamp.Time,
		Persistent:      hasPVC(pod),
		Storage:         pod.Annotations[annotationStorage],
		StorageClass:    pod.Annotations[annotationStorageClass],
		Parameters:      parametersFromAnnotations(pod.Annotations),
		Apps:            appsFromAnnotations(pod.Annotations),
		Schedule:        scheduleFromPod(pod),
		PortalURL:       pod.Annotations[annotationPortalURL],
		Labels:          copyStringMap(pod.Labels),
		Annotations:     copyStringMap(pod.Annotations),
		Spec:            *pod.Spec.DeepCopy(),
	}
	delete(state.Annotations, annotationState)
	state.Spec.NodeName = ""
	state.StoppedAt = parseTime(sts.Annotations[annotationStoppedAt])
	state.StoppedBySchedule = sts.Annotations[annotationStoppedBySchedule] == "true"
	stripExternalAuthEnv(&state.Spec, externalAuthEnvNames(pod))
	return state
}

func parametersFromAnnotations(annotations map[string]string) map[string]string {
	raw := annotations[annotationParameters]
	if raw == "" {
		return nil
	}
	var params map[string]string
	if err := json.Unmarshal([]byte(raw), &params); err != nil {
		return nil
	}
	return params
}

func scheduleFromAnnotations(annotations map[string]string) *Schedule {
	raw := annotations[annotationSchedule]
	if raw == "" {
		return nil
	}
	var schedule Schedule
	if err := json.Unmarshal([]byte(raw), &schedule); err != nil {
		return nil
	}
	return &schedule
}

func parseTime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

func (m *Manager) ensureGoverningService(ctx context.Context) error {
	_, err := m.Client.CoreV1().Services(m.Namespace).Get(ctx, GoverningService, metav1.GetOptions{})
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return err
	}
	_, err = m.Client.CoreV1().Services(m.Namespace).Create(ctx, &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      GoverningService,
			Namespace: m.Namespace,
			Labels: map[string]string{
				labelName:      AppName,
				labelManagedBy: AppName,
			},
		},
		Spec: corev1.ServiceSpec{
			ClusterIP: corev1.ClusterIPNone,
			Selector: map[string]string{
				labelName: AppName,
			},
			Ports: []corev1.ServicePort{
				{Name: "ssh", Port: 22, Protocol: corev1.ProtocolTCP},
			},
		},
	}, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		return nil
	}
	return err
}

func (m *Manager) getStatefulSet(ctx context.Context, name string) (*appsv1.StatefulSet, error) {
	return m.Client.AppsV1().StatefulSets(m.Namespace).Get(ctx, StatefulSetName(name), metav1.GetOptions{})
}

func (m *Manager) stopDynamicEnv(pod *corev1.Pod) {
	stripExternalAuthEnv(&pod.Spec, externalAuthEnvNames(pod))
}

func setStatefulSetStopped(sts *appsv1.StatefulSet, stoppedAt time.Time, scheduled bool) {
	if sts.Annotations == nil {
		sts.Annotations = map[string]string{}
	}
	sts.Annotations[annotationStoppedAt] = stoppedAt.UTC().Format(time.RFC3339)
	if scheduled {
		sts.Annotations[annotationStoppedBySchedule] = "true"
	} else {
		delete(sts.Annotations, annotationStoppedBySchedule)
	}
}

func clearStatefulSetStopped(sts *appsv1.StatefulSet) {
	delete(sts.Annotations, annotationStoppedAt)
	delete(sts.Annotations, annotationStoppedBySchedule)
}

func statefulSetReplicas(sts *appsv1.StatefulSet) int32 {
	if sts == nil || sts.Spec.Replicas == nil {
		return 0
	}
	return *sts.Spec.Replicas
}

func workspaceWithPhase(workspace Workspace, phase string) Workspace {
	workspace.Phase = phase
	return workspace
}

// MigrateLegacy converts stopped workspaces from the legacy poddy-state
// ConfigMap into stopped StatefulSets. Running bare pods are left alone until
// the next stop or restart, so the migration does not disrupt them.
func (m *Manager) MigrateLegacy(ctx context.Context) error {
	states, err := m.stateStore().Load(ctx)
	if err != nil {
		return err
	}
	if len(states) == 0 {
		return nil
	}
	migrated := map[string]bool{}
	serviceReady := false
	for name, state := range states {
		if err := validateName(name); err != nil {
			log.Printf("legacy workspace %q: %v", name, err)
			continue
		}
		if _, err := m.getStatefulSet(ctx, name); err == nil {
			migrated[name] = true
			continue
		} else if !apierrors.IsNotFound(err) {
			return err
		}
		if _, err := m.Get(ctx, name); err == nil {
			log.Printf("legacy workspace %q still has a bare pod; it will migrate on stop", name)
			continue
		} else if !apierrors.IsNotFound(err) {
			return err
		}
		if state.Name == "" {
			state.Name = name
		}
		pod := m.podFromState(state)
		m.stopDynamicEnv(pod)
		if pod.Annotations == nil {
			pod.Annotations = map[string]string{}
		}
		setAnnotation(pod.Annotations, annotationTemplateVersion, state.TemplateVersion)
		setAnnotation(pod.Annotations, annotationStorage, state.Storage)
		setAnnotation(pod.Annotations, annotationStorageClass, state.StorageClass)
		setAnnotation(pod.Annotations, annotationPortalURL, state.PortalURL)
		if len(state.Parameters) > 0 {
			if raw, err := json.Marshal(state.Parameters); err == nil {
				pod.Annotations[annotationParameters] = string(raw)
			}
		}
		if len(state.Apps) > 0 {
			pod.Annotations[annotationApps] = strings.Join(state.Apps, ",")
		}
		if state.Schedule != nil {
			if raw, err := json.Marshal(state.Schedule); err == nil {
				pod.Annotations[annotationSchedule] = string(raw)
			}
		}
		if !serviceReady {
			if err := m.ensureGoverningService(ctx); err != nil {
				return err
			}
			serviceReady = true
		}
		stoppedAt := state.StoppedAt
		if stoppedAt.IsZero() {
			stoppedAt = time.Now().UTC()
		}
		sts := m.buildStatefulSet(name, pod, 0)
		setStatefulSetStopped(sts, stoppedAt, state.StoppedBySchedule)
		if _, err := m.Client.AppsV1().StatefulSets(m.Namespace).Create(ctx, sts, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
			log.Printf("legacy workspace %q: create statefulset: %v", name, err)
			continue
		}
		migrated[name] = true
	}
	if len(migrated) == 0 {
		return nil
	}
	return m.stateStore().Update(ctx, func(states map[string]WorkspaceState) error {
		for name := range migrated {
			delete(states, name)
		}
		return nil
	})
}

func annotationsWithSchedule(annotations map[string]string, schedule *Schedule) map[string]string {
	out := copyStringMap(annotations)
	if out == nil {
		out = map[string]string{}
	}
	if schedule == nil || schedule.Empty() {
		delete(out, annotationSchedule)
		return out
	}
	raw, err := json.Marshal(schedule)
	if err == nil {
		out[annotationSchedule] = string(raw)
	}
	return out
}

func setAnnotation(annotations map[string]string, key, value string) {
	if value == "" {
		delete(annotations, key)
		return
	}
	annotations[key] = value
}
