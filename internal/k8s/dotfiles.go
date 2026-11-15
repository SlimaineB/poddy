package k8s

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"

	"poddy/internal/dotfiles"
)

const (
	envDotfilesURL    = "PODDY_DOTFILES_URL"
	envDotfilesBranch = "PODDY_DOTFILES_BRANCH"
)

// DotfilesFunc resolves the effective dotfiles repository for a workspace
// owner. The portal combines the per-user setting with its global fallback.
type DotfilesFunc func(ctx context.Context, owner string) (dotfiles.Config, error)

func (m *Manager) applyDotfiles(ctx context.Context, pod *corev1.Pod, owner string) error {
	if m.Dotfiles == nil {
		removeDotfilesEnv(pod)
		return nil
	}
	cfg, err := m.Dotfiles(ctx, owner)
	if err != nil {
		return fmt.Errorf("dotfiles: %w", err)
	}
	if cfg.Empty() {
		removeDotfilesEnv(pod)
		return nil
	}
	container := containerByName(pod, ContainerName)
	if container == nil {
		return nil
	}
	setEnv(container, envDotfilesURL, cfg.URL)
	setEnv(container, envDotfilesBranch, cfg.Branch)
	return nil
}

func removeDotfilesEnv(pod *corev1.Pod) {
	if pod == nil {
		return
	}
	for i := range pod.Spec.Containers {
		removeEnv(&pod.Spec.Containers[i], envDotfilesURL)
		removeEnv(&pod.Spec.Containers[i], envDotfilesBranch)
	}
}
