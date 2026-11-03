package k8s

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"

	"poddy/internal/templates"
)

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
	container := containerByName(pod, ContainerName)
	if container == nil {
		return fmt.Errorf("template must define a container named %q", ContainerName)
	}
	ids := make([]string, 0, len(requirements))
	for _, requirement := range requirements {
		ids = append(ids, requirement.ID)
	}
	env[envPortalURL] = portalURL
	env[envWorkspaceName] = workspace
	env[envExternalAuthIDs] = strings.Join(ids, ",")
	env[envGitAskpass] = "/usr/local/bin/poddy-git-askpass"
	env[envGitPrompt] = "0"
	if m.ExternalAuthToken != nil {
		token, tokenErr := m.ExternalAuthToken(workspace, owner)
		if tokenErr != nil {
			return tokenErr
		}
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
		return err
	}
	if pod.Annotations == nil {
		pod.Annotations = map[string]string{}
	}
	pod.Annotations[annotationExternalAuthEnv] = string(raw)
	return nil
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
	}
	for _, name := range names {
		remove[name] = true
	}
	for i := range spec.Containers {
		if spec.Containers[i].Name != ContainerName {
			continue
		}
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
