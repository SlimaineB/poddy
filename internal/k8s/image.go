package k8s

import (
	"strings"

	corev1 "k8s.io/api/core/v1"

	"poddy/internal/templates"
)

func rewritePodImages(pod *corev1.Pod, registry string, mappings map[string]string) {
	if registry == "" && len(mappings) == 0 {
		return
	}
	rewriter := templates.NewImageRewriter(registry, mappings)
	for i := range pod.Spec.InitContainers {
		pod.Spec.InitContainers[i].Image = rewriter.Rewrite(pod.Spec.InitContainers[i].Image)
	}
	for i := range pod.Spec.Containers {
		pod.Spec.Containers[i].Image = rewriter.Rewrite(pod.Spec.Containers[i].Image)
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
