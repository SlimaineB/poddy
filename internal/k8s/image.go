package k8s

import (
	"strings"

	corev1 "k8s.io/api/core/v1"

	"poddy/internal/templates"
)

func rewritePodImages(pod *corev1.Pod, registry string) {
	if registry == "" {
		return
	}
	for i := range pod.Spec.InitContainers {
		pod.Spec.InitContainers[i].Image = templates.RewriteImageRegistry(pod.Spec.InitContainers[i].Image, registry)
	}
	for i := range pod.Spec.Containers {
		pod.Spec.Containers[i].Image = templates.RewriteImageRegistry(pod.Spec.Containers[i].Image, registry)
	}
}

func applyImagePullSecrets(pod *corev1.Pod, secrets []string) {
	if len(secrets) == 0 {
		return
	}
	existing := make(map[string]bool, len(pod.Spec.ImagePullSecrets))
	for _, secret := range pod.Spec.ImagePullSecrets {
		existing[secret.Name] = true
	}
	for _, name := range secrets {
		name = strings.TrimSpace(name)
		if name == "" || existing[name] {
			continue
		}
		pod.Spec.ImagePullSecrets = append(pod.Spec.ImagePullSecrets, corev1.LocalObjectReference{Name: name})
		existing[name] = true
	}
}
