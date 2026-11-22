package k8s

import (
	"context"
	"errors"
	"fmt"

	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
)

// ReconcileAgents updates existing workspace StatefulSets with the current
// agent sidecar, probes, image, and mesh configuration. The StatefulSet update
// strategy is OnDelete, so running pods keep their old spec until they are
// recreated; new and restarted pods get the reconciled template.
func (m *Manager) ReconcileAgents(ctx context.Context) error {
	list, err := m.Client.AppsV1().StatefulSets(m.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("list workspace statefulsets: %w", err)
	}
	var errs []error
	for i := range list.Items {
		name := workspaceName(&list.Items[i])
		if name == "" {
			continue
		}
		err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			sts, err := m.getStatefulSet(ctx, name)
			if err != nil {
				return err
			}
			pod := podFromStatefulSet(sts)
			if err := m.applyAgentEnv(ctx, pod, name, stateFromStatefulSet(sts).Owner); err != nil {
				return err
			}
			if equality.Semantic.DeepEqual(sts.Spec.Template.Spec, pod.Spec) {
				return nil
			}
			sts.Spec.Template.Spec = pod.Spec
			_, err = m.Client.AppsV1().StatefulSets(m.Namespace).Update(ctx, sts, metav1.UpdateOptions{})
			return err
		})
		if apierrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("reconcile workspace %s: %w", name, err))
		}
	}
	return errors.Join(errs...)
}
