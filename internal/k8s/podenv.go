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

func (m *Manager) applyAgentEnv(ctx context.Context, pod *corev1.Pod, workspace, owner string) error {
	applyOOMProtection(pod)

	container := containerByName(pod, ContainerName)
	if container == nil {
		return nil
	}
	useWSS := m.AgentURL != "" && m.AgentToken != nil
	useMesh := strings.TrimSpace(m.MeshAuthKey) != ""
	if !useWSS && !useMesh {
		removeEnv(container, envAgentSidecar)
		removeAgentEnv(pod)
		removeAgentTokenSecretMount(pod)
		removeContainer(pod, AgentContainerName)
		return m.deleteAgentTokenSecret(ctx, workspace)
	}

	removeEnv(container, envAgentURL)
	removeEnv(container, envAgentToken)
	removeEnv(container, envAgentTokenFile)
	removeEnv(container, envAgentRefreshFile)
	removeMeshEnv(container)
	removeAgentTokenSecretMountFromContainer(container)

	if m.AgentImage == "" {
		removeEnv(container, envAgentSidecar)
		removeContainer(pod, AgentContainerName)
		setEnv(container, envWorkspaceName, workspace)
		if useWSS {
			token, err := m.AgentToken(workspace, owner)
			if err != nil {
				return err
			}
			secretName := AgentTokenSecretName(workspace)
			if err := m.ensureAgentTokenSecret(ctx, workspace, token); err != nil {
				return err
			}
			setEnv(container, envAgentURL, m.AgentURL)
			setEnvFromSecret(container, envAgentToken, secretName, agentTokenSecretKey)
			setEnv(container, envAgentTokenFile, agentTokenFile)
			setEnv(container, envAgentRefreshFile, agentTokenRefreshFile)
			mountAgentTokenSecret(pod, container, workspace)
		} else {
			removeAgentTokenSecretMount(pod)
			if err := m.deleteAgentTokenSecret(ctx, workspace); err != nil {
				return err
			}
		}
		mountAgentTokenState(pod, container)
		applyMeshEnv(container, m.MeshControlURL, m.MeshAuthKey)
		return nil
	}

	setEnv(container, envAgentSidecar, "true")
	secretName := ""
	if useWSS {
		token, err := m.AgentToken(workspace, owner)
		if err != nil {
			return err
		}
		secretName = AgentTokenSecretName(workspace)
		if err := m.ensureAgentTokenSecret(ctx, workspace, token); err != nil {
			return err
		}
	} else {
		removeAgentTokenSecretMount(pod)
		if err := m.deleteAgentTokenSecret(ctx, workspace); err != nil {
			return err
		}
	}
	upsertAgentSidecar(pod, m.agentSidecarImage(), m.AgentURL, workspace, secretName, m.MeshControlURL, m.MeshAuthKey)
	return nil
}

func removeMeshEnv(container *corev1.Container) {
	removeEnv(container, envMeshControlURL)
	removeEnv(container, envMeshAuthKey)
	removeEnv(container, envMeshStateDir)
}

func applyMeshEnv(container *corev1.Container, controlURL, authKey string) {
	if container == nil || strings.TrimSpace(authKey) == "" {
		return
	}
	setEnv(container, envMeshControlURL, strings.TrimSpace(controlURL))
	setEnv(container, envMeshAuthKey, strings.TrimSpace(authKey))
	setEnv(container, envMeshStateDir, agentStateMountPath+"/tsnet")
}

func (m *Manager) agentSidecarImage() string {
	return templates.RewriteImageRegistryWithMap(m.AgentImage, m.ImageRegistry, m.ImageRegistryMap)
}

const (
	agentStateVolumeName = "poddy-agent-state"
	agentStateMountPath  = "/var/lib/poddy"
	agentTokenFile       = agentStateMountPath + "/agent-token"
)

func upsertAgentSidecar(pod *corev1.Pod, image, agentURL, workspace, secretName, meshControlURL, meshAuthKey string) {
	container := agentSidecar(image, agentURL, workspace, secretName, meshControlURL, meshAuthKey)
	mountAgentTokenState(pod, &container)
	if secretName != "" {
		mountAgentTokenSecret(pod, &container, workspace)
	}
	for i := range pod.Spec.Containers {
		if pod.Spec.Containers[i].Name == AgentContainerName {
			pod.Spec.Containers[i] = container
			return
		}
	}
	pod.Spec.Containers = append(pod.Spec.Containers, container)
}

// mountAgentTokenState gives the agent a writable emptyDir so a token renewed
// after a container restart survives in the same pod. A new pod always gets a
// fresh token from the control plane.
func mountAgentTokenState(pod *corev1.Pod, container *corev1.Container) {
	if container == nil {
		return
	}
	found := false
	for i := range pod.Spec.Volumes {
		if pod.Spec.Volumes[i].Name == agentStateVolumeName {
			found = true
			break
		}
	}
	if !found {
		pod.Spec.Volumes = append(pod.Spec.Volumes, corev1.Volume{
			Name: agentStateVolumeName,
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		})
	}
	for _, mount := range container.VolumeMounts {
		if mount.Name == agentStateVolumeName {
			return
		}
	}
	container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
		Name:      agentStateVolumeName,
		MountPath: agentStateMountPath,
	})
}

// mountAgentTokenSecret projects the token Secret into the pod so kubelet
// refreshes the file when the control plane rewrites the Secret.
func mountAgentTokenSecret(pod *corev1.Pod, container *corev1.Container, workspace string) {
	if container == nil {
		return
	}
	found := false
	for i := range pod.Spec.Volumes {
		if pod.Spec.Volumes[i].Name == agentTokenVolumeName {
			pod.Spec.Volumes[i].VolumeSource = corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName:  AgentTokenSecretName(workspace),
					DefaultMode: ptr.To[int32](0o444),
				},
			}
			found = true
			break
		}
	}
	if !found {
		pod.Spec.Volumes = append(pod.Spec.Volumes, corev1.Volume{
			Name: agentTokenVolumeName,
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName:  AgentTokenSecretName(workspace),
					DefaultMode: ptr.To[int32](0o444),
				},
			},
		})
	}
	for _, mount := range container.VolumeMounts {
		if mount.Name == agentTokenVolumeName {
			return
		}
	}
	container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
		Name:      agentTokenVolumeName,
		ReadOnly:  true,
		MountPath: agentTokenMountPath,
	})
}

func removeAgentTokenSecretMount(pod *corev1.Pod) {
	kept := pod.Spec.Volumes[:0]
	for _, volume := range pod.Spec.Volumes {
		if volume.Name != agentTokenVolumeName {
			kept = append(kept, volume)
		}
	}
	pod.Spec.Volumes = kept
	for i := range pod.Spec.Containers {
		removeAgentTokenSecretMountFromContainer(&pod.Spec.Containers[i])
	}
}

func removeAgentTokenSecretMountFromContainer(container *corev1.Container) {
	kept := container.VolumeMounts[:0]
	for _, mount := range container.VolumeMounts {
		if mount.Name != agentTokenVolumeName {
			kept = append(kept, mount)
		}
	}
	container.VolumeMounts = kept
}

func agentSidecar(image, agentURL, workspace, secretName, meshControlURL, meshAuthKey string) corev1.Container {
	env := []corev1.EnvVar{
		{Name: envWorkspaceName, Value: workspace},
	}
	if agentURL != "" {
		env = append(env,
			corev1.EnvVar{Name: envAgentURL, Value: agentURL},
			corev1.EnvVar{Name: envAgentTokenFile, Value: agentTokenFile},
			corev1.EnvVar{Name: envAgentRefreshFile, Value: agentTokenRefreshFile},
		)
		if secretName != "" {
			env = append(env, secretEnv(envAgentToken, secretName, agentTokenSecretKey))
		}
	}
	if strings.TrimSpace(meshAuthKey) != "" {
		env = append(env,
			corev1.EnvVar{Name: envMeshControlURL, Value: strings.TrimSpace(meshControlURL)},
			corev1.EnvVar{Name: envMeshAuthKey, Value: strings.TrimSpace(meshAuthKey)},
			corev1.EnvVar{Name: envMeshStateDir, Value: agentStateMountPath + "/tsnet"},
		)
	}
	return corev1.Container{
		Name:            AgentContainerName,
		Image:           image,
		ImagePullPolicy: corev1.PullIfNotPresent,
		Command:         []string{"/usr/local/bin/poddy-agent"},
		Env:             env,
		SecurityContext: &corev1.SecurityContext{
			RunAsNonRoot:             ptr.To(true),
			RunAsUser:                ptr.To[int64](1000),
			RunAsGroup:               ptr.To[int64](1000),
			AllowPrivilegeEscalation: ptr.To(false),
			ReadOnlyRootFilesystem:   ptr.To(true),
			Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
		},
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("20m"),
				corev1.ResourceMemory: resource.MustParse("64Mi"),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("500m"),
				corev1.ResourceMemory: resource.MustParse("256Mi"),
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
		removeEnv(&pod.Spec.Containers[i], envAgentTokenFile)
		removeEnv(&pod.Spec.Containers[i], envAgentRefreshFile)
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
		envPortalURL:        true,
		envWorkspaceName:    true,
		envWorkspaceToken:   true,
		envExternalAuthIDs:  true,
		envGitAskpass:       true,
		envGitPrompt:        true,
		envAgentURL:         true,
		envAgentToken:       true,
		envAgentTokenFile:   true,
		envAgentRefreshFile: true,
		envAgentSidecar:     true,
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

func setEnvFromSecret(container *corev1.Container, name, secretName, key string) {
	valueFrom := &corev1.EnvVarSource{
		SecretKeyRef: &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: secretName},
			Key:                  key,
		},
	}
	for i := range container.Env {
		if container.Env[i].Name == name {
			container.Env[i].Value = ""
			container.Env[i].ValueFrom = valueFrom
			return
		}
	}
	container.Env = append(container.Env, corev1.EnvVar{Name: name, ValueFrom: valueFrom})
}

func secretEnv(name, secretName, key string) corev1.EnvVar {
	return corev1.EnvVar{
		Name: name,
		ValueFrom: &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: secretName},
				Key:                  key,
			},
		},
	}
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
