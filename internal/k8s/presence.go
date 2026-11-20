package k8s

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	coordinationv1 "k8s.io/api/coordination/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	presenceAddressAnnotation = "poddy.io/address"
	presenceLeaseSeconds      = int32(30)
	presenceMaxAge            = 45 * time.Second
)

// LeasePresence stores tunnel ownership in Kubernetes Leases so every portal
// replica can find the replica holding a workspace agent or region tunnel.
type LeasePresence struct {
	Client     kubernetes.Interface
	Namespace  string
	InstanceID string
}

func (p *LeasePresence) Claim(ctx context.Context, kind, name, address string) error {
	if p == nil || p.Client == nil {
		return fmt.Errorf("presence: kubernetes client is required")
	}
	leases := p.Client.CoordinationV1().Leases(p.Namespace)
	leaseName := presenceLeaseName(kind, name)
	now := metav1.NewMicroTime(time.Now())
	duration := presenceLeaseSeconds
	lease, err := leases.Get(ctx, leaseName, metav1.GetOptions{})
	if err != nil {
		lease = &coordinationv1.Lease{
			ObjectMeta: metav1.ObjectMeta{
				Name:        leaseName,
				Namespace:   p.Namespace,
				Annotations: map[string]string{presenceAddressAnnotation: address},
			},
			Spec: coordinationv1.LeaseSpec{
				HolderIdentity:       &p.InstanceID,
				LeaseDurationSeconds: &duration,
				RenewTime:            &now,
			},
		}
		if _, err := leases.Create(ctx, lease, metav1.CreateOptions{}); err != nil {
			return err
		}
		return nil
	}
	if lease.Annotations == nil {
		lease.Annotations = map[string]string{}
	}
	lease.Annotations[presenceAddressAnnotation] = address
	lease.Spec.HolderIdentity = &p.InstanceID
	lease.Spec.LeaseDurationSeconds = &duration
	lease.Spec.RenewTime = &now
	if _, err := leases.Update(ctx, lease, metav1.UpdateOptions{}); err != nil {
		return err
	}
	return nil
}

func (p *LeasePresence) Release(ctx context.Context, kind, name string) error {
	if p == nil || p.Client == nil {
		return nil
	}
	leases := p.Client.CoordinationV1().Leases(p.Namespace)
	leaseName := presenceLeaseName(kind, name)
	lease, err := leases.Get(ctx, leaseName, metav1.GetOptions{})
	if err != nil {
		return nil
	}
	if lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity != p.InstanceID {
		return nil
	}
	return leases.Delete(ctx, leaseName, metav1.DeleteOptions{})
}

func (p *LeasePresence) Lookup(ctx context.Context, kind, name string) (string, bool, error) {
	if p == nil || p.Client == nil {
		return "", false, nil
	}
	lease, err := p.Client.CoordinationV1().Leases(p.Namespace).Get(ctx, presenceLeaseName(kind, name), metav1.GetOptions{})
	if err != nil {
		return "", false, nil
	}
	if lease.Spec.RenewTime == nil || time.Since(lease.Spec.RenewTime.Time) > presenceMaxAge {
		return "", false, nil
	}
	address := strings.TrimSpace(lease.Annotations[presenceAddressAnnotation])
	if address == "" {
		return "", false, nil
	}
	return address, true, nil
}

func presenceLeaseName(kind, name string) string {
	cleaned := strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	lastDash := false
	for _, r := range cleaned {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	suffix := strings.Trim(b.String(), "-")
	if suffix == "" {
		suffix = "unnamed"
	}
	prefix := "poddy-tun-" + kind + "-"
	full := prefix + suffix
	if len(full) <= 63 {
		return full
	}
	sum := sha256.Sum256([]byte(full))
	keep := 63 - len(prefix) - 1 - 12
	if keep < 1 {
		keep = 1
	}
	return prefix + suffix[:keep] + "-" + hex.EncodeToString(sum[:6])
}
