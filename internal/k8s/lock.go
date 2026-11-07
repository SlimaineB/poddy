package k8s

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"time"

	coordinationv1 "k8s.io/api/coordination/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	lockPrefix  = "poddy-lock-"
	lockTimeout = 15 * time.Second
)

var lockDuration = int32(120)

func (m *Manager) lockWorkspace(ctx context.Context, name string) (func(), error) {
	holder := m.InstanceID
	if holder == "" {
		holder, _ = os.Hostname()
	}
	if holder == "" {
		holder = "poddy"
	}
	leaseName := workspaceLeaseName(name)
	deadline := time.Now().Add(lockTimeout)
	for {
		lease, err := m.Client.CoordinationV1().Leases(m.Namespace).Get(ctx, leaseName, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			now := metav1.NewMicroTime(time.Now())
			_, err = m.Client.CoordinationV1().Leases(m.Namespace).Create(ctx, &coordinationv1.Lease{
				ObjectMeta: metav1.ObjectMeta{Name: leaseName, Namespace: m.Namespace},
				Spec: coordinationv1.LeaseSpec{
					HolderIdentity:       &holder,
					LeaseDurationSeconds: &lockDuration,
					AcquireTime:          &now,
					RenewTime:            &now,
				},
			}, metav1.CreateOptions{})
			if err == nil {
				return m.unlockFunc(leaseName, holder), nil
			}
			if !apierrors.IsAlreadyExists(err) && !apierrors.IsConflict(err) {
				return nil, fmt.Errorf("workspace lock %s: %w", name, err)
			}
		} else if err != nil {
			return nil, fmt.Errorf("workspace lock %s: %w", name, err)
		} else if lockAvailable(lease, holder) {
			now := metav1.NewMicroTime(time.Now())
			lease.Spec.HolderIdentity = &holder
			lease.Spec.LeaseDurationSeconds = &lockDuration
			lease.Spec.RenewTime = &now
			if lease.Spec.AcquireTime == nil {
				lease.Spec.AcquireTime = &now
			}
			updated, updateErr := m.Client.CoordinationV1().Leases(m.Namespace).Update(ctx, lease, metav1.UpdateOptions{})
			if updateErr == nil {
				return m.unlockFunc(updated.Name, holder), nil
			}
			if !apierrors.IsConflict(updateErr) {
				return nil, fmt.Errorf("workspace lock %s: %w", name, updateErr)
			}
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("workspace %s is locked by another operation", name)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func (m *Manager) unlockFunc(name, holder string) func() {
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		lease, err := m.Client.CoordinationV1().Leases(m.Namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil || lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity != holder {
			return
		}
		empty := ""
		lease.Spec.HolderIdentity = &empty
		lease.Spec.RenewTime = nil
		_, _ = m.Client.CoordinationV1().Leases(m.Namespace).Update(ctx, lease, metav1.UpdateOptions{})
	}
}

func lockAvailable(lease *coordinationv1.Lease, holder string) bool {
	if lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity == "" || *lease.Spec.HolderIdentity == holder {
		return true
	}
	if lease.Spec.RenewTime == nil || lease.Spec.LeaseDurationSeconds == nil {
		return true
	}
	expires := lease.Spec.RenewTime.Time.Add(time.Duration(*lease.Spec.LeaseDurationSeconds) * time.Second)
	return time.Now().After(expires)
}

func workspaceLeaseName(name string) string {
	candidate := lockPrefix + name
	if len(candidate) <= 63 {
		return candidate
	}
	sum := sha256.Sum256([]byte(name))
	return lockPrefix + hex.EncodeToString(sum[:])[:52]
}
