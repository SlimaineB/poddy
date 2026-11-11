package k8s

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/utils/ptr"

	"poddy/internal/templates"
)

func (m *Manager) applyAgentEnv(pod *corev1.Pod, workspace, owner string) error {
	applyOOMProtection(pod)

	container := containerByName(pod, ContainerName)
	if container == nil {
		return nil
	}
	if m.AgentURL == "" || m.AgentToken == nil {
		removeEnv(container, envAgentSidecar)
		removeAgentEnv(pod)
		removeContainer(pod, AgentContainerName)
		return nil
	}
	token, err := m.AgentToken(workspace, owner)
	if err != nil {
		return err
	}
	if m.AgentImage == "" {
		setEnv(container, envAgentURL, m.AgentURL)
		setEnv(container, envAgentToken, token)
		setEnv(container, envWorkspaceName, workspace)
		removeEnv(container, envAgentSidecar)
		removeContainer(pod, AgentContainerName)
		return nil
	}

	removeEnv(container, envAgentURL)
	removeEnv(container, envAgentToken)
	setEnv(container, envAgentSidecar, "true")
	upsertAgentSidecar(pod, m.agentSidecarImage(), m.AgentURL, workspace, token)
	return nil
}

func (m *Manager) agentSidecarImage() string {
	return templates.RewriteImageRegistryWithMap(m.AgentImage, m.ImageRegistry, m.ImageRegistryMap)
}

func upsertAgentSidecar(pod *corev1.Pod, image, agentURL, workspace, token string) {
	container := agentSidecar(image, agentURL, workspace, token)
	for i := range pod.Spec.Containers {
		if pod.Spec.Containers[i].Name == AgentContainerName {
			pod.Spec.Containers[i] = container
			return
		}
	}
	pod.Spec.Containers = append(pod.Spec.Containers, container)
}

func agentSidecar(image, agentURL, workspace, token string) corev1.Container {
	return corev1.Container{
		Name:            AgentContainerName,
		Image:           image,
		ImagePullPolicy: corev1.PullIfNotPresent,
		Command:         []string{"/usr/local/bin/poddy-agent"},
		Env: []corev1.EnvVar{
			{Name: envAgentURL, Value: agentURL},
			{Name: envAgentToken, Value: token},
			{Name: envWorkspaceName, Value: workspace},
		},
		SecurityContext: &corev1.SecurityContext{
			RunAsNonRoot:             ptr.To(true),
			RunAsUser:                ptr.To[int64](65532),
			RunAsGroup:               ptr.To[int64](65532),
			AllowPrivilegeEscalation: ptr.To(false),
			ReadOnlyRootFilesystem:   ptr.To(true),
			Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
		},
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("10m"),
				corev1.ResourceMemory: resource.MustParse("16Mi"),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("500m"),
				corev1.ResourceMemory: resource.MustParse("128Mi"),
			},
		},
	}
}

// applyOOMProtection lets poddy-ssh-start lower sshd's OOM score. User shells
// are reset to the default score by the poddy-shell wrapper, so workspace
// workloads stay killable during memory spikes.
func applyOOMProtection(pod *corev1.Pod) {
	container := containerByName(pod, ContainerName)
	if container == nil {
		return
	}
	if container.SecurityContext == nil {
		container.SecurityContext = &corev1.SecurityContext{}
	}
	if container.SecurityContext.Capabilities == nil {
		container.SecurityContext.Capabilities = &corev1.Capabilities{}
	}
	caps := container.SecurityContext.Capabilities
	kept := caps.Drop[:0]
	for _, capability := range caps.Drop {
		if capability != "SYS_RESOURCE" {
			kept = append(kept, capability)
		}
	}
	caps.Drop = kept
	for _, capability := range caps.Add {
		if capability == "SYS_RESOURCE" {
			return
		}
	}
	caps.Add = append(caps.Add, "SYS_RESOURCE")
}

func removeAgentEnv(pod *corev1.Pod) {
	for i := range pod.Spec.Containers {
		removeEnv(&pod.Spec.Containers[i], envAgentURL)
		removeEnv(&pod.Spec.Containers[i], envAgentToken)
	}
}

func removeContainer(pod *corev1.Pod, name string) {
	kept := pod.Spec.Containers[:0]
	for _, container := range pod.Spec.Containers {
		if container.Name != name {
			kept = append(kept, container)
		}
	}
	pod.Spec.Containers = kept
}

func (m *Manager) applyExternalAuth(ctx context.Context, pod *corev1.Pod, owner, workspace, portalURL string, requirements []templates.ExternalAuth) error {
	if m.ExternalAuth == nil || len(requirements) == 0 {
		return nil
	}
	if portalURL == "" {
		portalURL = m.PortalURL
	}
	env, err := m.ExternalAuth(ctx, owner, workspace, portalURL, requirements)
	if err != nil {
		return err
	}
	if len(env) == 0 {
		return nil
	}
	token := ""
	if m.ExternalAuthToken != nil {
		var tokenErr error
		token, tokenErr = m.ExternalAuthToken(workspace, owner)
		if tokenErr != nil {
			return tokenErr
		}
	}
	applyExternalAuthEnv(pod, owner, workspace, portalURL, requirements, env, token)
	return nil
}

func applyExternalAuthEnv(pod *corev1.Pod, owner, workspace, portalURL string, requirements []templates.ExternalAuth, env map[string]string, token string) {
	if len(env) == 0 && token == "" {
		return
	}
	container := containerByName(pod, ContainerName)
	if container == nil {
		return
	}
	if env == nil {
		env = map[string]string{}
	}
	ids := make([]string, 0, len(requirements))
	for _, requirement := range requirements {
		ids = append(ids, requirement.ID)
	}
	if portalURL == "" {
		portalURL = env[envPortalURL]
	}
	if portalURL != "" {
		env[envPortalURL] = portalURL
	}
	if workspace != "" {
		env[envWorkspaceName] = workspace
	}
	if len(ids) > 0 {
		env[envExternalAuthIDs] = strings.Join(ids, ",")
	}
	if len(env) > 0 {
		env[envGitAskpass] = "/usr/local/bin/poddy-git-askpass"
		env[envGitPrompt] = "0"
	}
	if token != "" {
		env[envWorkspaceToken] = token
	}
	names := make([]string, 0, len(env))
	for name, value := range env {
		setEnv(container, name, value)
		names = append(names, name)
	}
	sort.Strings(names)
	raw, err := json.Marshal(names)
	if err != nil {
		return
	}
	if pod.Annotations == nil {
		pod.Annotations = map[string]string{}
	}
	pod.Annotations[annotationExternalAuthEnv] = string(raw)
}

func externalAuthEnvNames(pod *corev1.Pod) []string {
	if pod == nil || pod.Annotations == nil {
		return nil
	}
	var names []string
	if raw := pod.Annotations[annotationExternalAuthEnv]; raw != "" {
		_ = json.Unmarshal([]byte(raw), &names)
	}
	return names
}

func stripExternalAuthEnv(spec *corev1.PodSpec, names []string) {
	remove := map[string]bool{
		envPortalURL:       true,
		envWorkspaceName:   true,
		envWorkspaceToken:  true,
		envExternalAuthIDs: true,
		envGitAskpass:      true,
		envGitPrompt:       true,
		envAgentURL:        true,
		envAgentToken:      true,
		envAgentSidecar:    true,
	}
	for _, name := range names {
		remove[name] = true
	}
	for i := range spec.Containers {
		kept := spec.Containers[i].Env[:0]
		for _, item := range spec.Containers[i].Env {
			if !remove[item.Name] {
				kept = append(kept, item)
			}
		}
		spec.Containers[i].Env = kept
	}
}

func containerByName(pod *corev1.Pod, name string) *corev1.Container {
	for i := range pod.Spec.Containers {
		if pod.Spec.Containers[i].Name == name {
			return &pod.Spec.Containers[i]
		}
	}
	return nil
}

func envValue(pod *corev1.Pod, containerName, name string) string {
	container := containerByName(pod, containerName)
	if container == nil {
		return ""
	}
	for _, item := range container.Env {
		if item.Name == name {
			return item.Value
		}
	}
	return ""
}

func setEnv(container *corev1.Container, name, value string) {
	for i := range container.Env {
		if container.Env[i].Name == name {
			container.Env[i].Value = value
			container.Env[i].ValueFrom = nil
			return
		}
	}
	container.Env = append(container.Env, corev1.EnvVar{Name: name, Value: value})
}

func removeEnv(container *corev1.Container, name string) {
	kept := container.Env[:0]
	for _, item := range container.Env {
		if item.Name != name {
			kept = append(kept, item)
		}
	}
	container.Env = kept
}
