package k8s

import (
	"context"
	"fmt"
	"sort"
	"strings"

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

// Volume describes a workspace persistent volume claim and the PV bound to it.
type Volume struct {
	Workspace    string   `json:"workspace"`
	Name         string   `json:"name"`
	Phase        string   `json:"phase,omitempty"`
	StorageClass string   `json:"storageClass,omitempty"`
	Capacity     string   `json:"capacity,omitempty"`
	Requested    string   `json:"requested,omitempty"`
	Bytes        int64    `json:"bytes,omitempty"`
	AccessModes  []string `json:"accessModes,omitempty"`
	VolumeName   string   `json:"volumeName,omitempty"`
}

// ListVolumes returns the PVCs managed for workspaces in the manager namespace.
func (m *Manager) ListVolumes(ctx context.Context) ([]Volume, error) {
	claims, err := m.Client.CoreV1().PersistentVolumeClaims(m.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelName + "=" + AppName,
	})
	if err != nil {
		return nil, err
	}

	out := make([]Volume, 0, len(claims.Items))
	for i := range claims.Items {
		pvc := &claims.Items[i]
		workspace := pvc.Labels[labelWorkspace]
		if workspace == "" {
			workspace = strings.TrimPrefix(pvc.Name, "poddy-")
		}
		capacity := pvc.Status.Capacity.Storage()
		if capacity == nil || capacity.IsZero() {
			capacity = pvc.Spec.Resources.Requests.Storage()
		}
		requested := pvc.Spec.Resources.Requests.Storage()
		accessModes := make([]string, 0, len(pvc.Spec.AccessModes))
		for _, mode := range pvc.Spec.AccessModes {
			accessModes = append(accessModes, string(mode))
		}
		class := ""
		if pvc.Spec.StorageClassName != nil {
			class = *pvc.Spec.StorageClassName
		}
		volume := Volume{
			Workspace:    workspace,
			Name:         pvc.Name,
			Phase:        string(pvc.Status.Phase),
			StorageClass: class,
			AccessModes:  accessModes,
			VolumeName:   pvc.Spec.VolumeName,
		}
		if capacity != nil {
			volume.Capacity = capacity.String()
			volume.Bytes = capacity.Value()
		}
		if requested != nil {
			volume.Requested = requested.String()
		}
		out = append(out, volume)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Workspace == out[j].Workspace {
			return out[i].Name < out[j].Name
		}
		return out[i].Workspace < out[j].Workspace
	})
	return out, nil
}
