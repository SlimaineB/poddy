package k8s

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (m *Manager) createPVC(ctx context.Context, name, templateName, storage, storageClass string) (bool, error) {
	quantity, err := resource.ParseQuantity(storage)
	if err != nil {
		return false, fmt.Errorf("invalid storage %q: %w", storage, err)
	}

	var class *string
	if storageClass != "" {
		class = &storageClass
	}

	pvcLabels := labels(name)
	pvcLabels[labelTemplate] = templateName

	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:   PodName(name),
			Labels: pvcLabels,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			StorageClassName: class,
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: quantity,
				},
			},
		},
	}

	_, err = m.Client.CoreV1().PersistentVolumeClaims(m.Namespace).Create(ctx, pvc, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (m *Manager) deletePVC(ctx context.Context, name string) error {
	err := m.Client.CoreV1().PersistentVolumeClaims(m.Namespace).Delete(ctx, PodName(name), metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}
