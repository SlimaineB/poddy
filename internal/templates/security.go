package templates

import (
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
)

var allowedWorkspaceCapabilities = map[corev1.Capability]bool{
	"SYS_RESOURCE": true,
}

func validateWorkspacePod(pod *corev1.Pod, expectedPVC string) error {
	if pod.Spec.HostNetwork {
		return fmt.Errorf("hostNetwork is not allowed")
	}
	if pod.Spec.HostPID {
		return fmt.Errorf("hostPID is not allowed")
	}
	if pod.Spec.HostIPC {
		return fmt.Errorf("hostIPC is not allowed")
	}
	if pod.Spec.ServiceAccountName != "" {
		return fmt.Errorf("serviceAccountName is not allowed")
	}
	if pod.Spec.AutomountServiceAccountToken != nil && *pod.Spec.AutomountServiceAccountToken {
		return fmt.Errorf("automountServiceAccountToken must be false")
	}
	if pod.Spec.PriorityClassName != "" {
		return fmt.Errorf("priorityClassName is not allowed")
	}
	if pod.Spec.SecurityContext != nil && len(pod.Spec.SecurityContext.Sysctls) > 0 {
		return fmt.Errorf("sysctls are not allowed")
	}
	if pod.Spec.SecurityContext != nil && pod.Spec.SecurityContext.SeccompProfile != nil && pod.Spec.SecurityContext.SeccompProfile.Type == corev1.SeccompProfileTypeUnconfined {
		return fmt.Errorf("unconfined seccomp profile is not allowed")
	}

	seenVolumes := map[string]bool{}
	for _, volume := range pod.Spec.Volumes {
		seenVolumes[volume.Name] = true
		switch {
		case volume.EmptyDir != nil:
		case volume.ConfigMap != nil:
		case volume.PersistentVolumeClaim != nil:
			if expectedPVC == "" {
				return fmt.Errorf("persistent volume claim %q is not allowed for ephemeral storage", volume.Name)
			}
			if volume.PersistentVolumeClaim.ClaimName != expectedPVC {
				return fmt.Errorf("volume %q must use claim %q", volume.Name, expectedPVC)
			}
		case volume.HostPath != nil:
			return fmt.Errorf("hostPath volume %q is not allowed", volume.Name)
		case volume.Secret != nil:
			return fmt.Errorf("secret volume %q is not allowed", volume.Name)
		case volume.Projected != nil:
			return fmt.Errorf("projected volume %q is not allowed", volume.Name)
		default:
			return fmt.Errorf("volume %q uses an unsupported source", volume.Name)
		}
	}

	containers := make([]corev1.Container, 0, len(pod.Spec.InitContainers)+len(pod.Spec.Containers))
	containers = append(containers, pod.Spec.InitContainers...)
	containers = append(containers, pod.Spec.Containers...)
	for _, container := range containers {
		if err := validateWorkspaceContainer(container, seenVolumes); err != nil {
			return err
		}
	}
	return nil
}

func validateWorkspaceContainer(container corev1.Container, volumes map[string]bool) error {
	for _, port := range container.Ports {
		if port.HostPort != 0 || port.HostIP != "" {
			return fmt.Errorf("container %q must not use hostPort/hostIP", container.Name)
		}
	}

	for _, env := range container.Env {
		if env.ValueFrom == nil {
			continue
		}
		if env.ValueFrom.SecretKeyRef != nil {
			return fmt.Errorf("container %q must not read secrets through env", container.Name)
		}
	}
	for _, envFrom := range container.EnvFrom {
		if envFrom.SecretRef != nil {
			return fmt.Errorf("container %q must not read secrets through envFrom", container.Name)
		}
	}
	for _, mount := range container.VolumeMounts {
		if !volumes[mount.Name] {
			return fmt.Errorf("container %q mounts unknown volume %q", container.Name, mount.Name)
		}
	}

	security := container.SecurityContext
	if security == nil {
		return nil
	}
	if security.Privileged != nil && *security.Privileged {
		return fmt.Errorf("container %q must not be privileged", container.Name)
	}
	if security.ProcMount != nil && *security.ProcMount != corev1.DefaultProcMount {
		return fmt.Errorf("container %q must not override procMount", container.Name)
	}
	if security.SeccompProfile != nil && security.SeccompProfile.Type == corev1.SeccompProfileTypeUnconfined {
		return fmt.Errorf("container %q must not use an unconfined seccomp profile", container.Name)
	}
	if security.WindowsOptions != nil && security.WindowsOptions.HostProcess != nil && *security.WindowsOptions.HostProcess {
		return fmt.Errorf("container %q must not use Windows hostProcess", container.Name)
	}
	if security.Capabilities != nil {
		for _, capability := range security.Capabilities.Add {
			if !allowedWorkspaceCapabilities[corev1.Capability(strings.ToUpper(string(capability)))] {
				return fmt.Errorf("container %q must not add capability %q", container.Name, capability)
			}
		}
	}
	return nil
}
