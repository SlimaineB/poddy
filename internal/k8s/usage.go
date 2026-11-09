package k8s

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

// ResourceUsage is the sum of the resource requests for one workspace.
type ResourceUsage struct {
	CPU     resource.Quantity
	Memory  resource.Quantity
	Storage resource.Quantity
}

func (u ResourceUsage) Add(other ResourceUsage) ResourceUsage {
	u.CPU.Add(other.CPU)
	u.Memory.Add(other.Memory)
	u.Storage.Add(other.Storage)
	return u
}

func resourceUsage(spec corev1.PodSpec, storage string) (ResourceUsage, error) {
	cpu, memory := resourceRequests(spec)
	var usage ResourceUsage
	usage.CPU = cpu
	usage.Memory = memory
	if storage == "" {
		return usage, nil
	}
	quantity, err := resource.ParseQuantity(storage)
	if err != nil {
		return ResourceUsage{}, fmt.Errorf("invalid storage %q: %w", storage, err)
	}
	usage.Storage = quantity
	return usage, nil
}
