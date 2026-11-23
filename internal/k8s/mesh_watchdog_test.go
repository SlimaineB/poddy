package k8s

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestRecreateUnhealthyMeshPods(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name       string
		ready      bool
		restarts   int32
		unreadyFor time.Duration
		deleted    bool
	}{
		{name: "unhealthy", ready: false, restarts: 1, unreadyFor: 3 * time.Minute, deleted: true},
		{name: "never-restarted", ready: false, restarts: 0, unreadyFor: 3 * time.Minute},
		{name: "healthy", ready: true, restarts: 1, unreadyFor: 3 * time.Minute},
		{name: "recently-unready", ready: false, restarts: 1, unreadyFor: 10 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pod := meshTestPod(tc.name, tc.ready, tc.restarts, now.Add(-tc.unreadyFor))
			client := fake.NewSimpleClientset(pod)
			manager := &Manager{Client: client, Namespace: "poddy"}

			if err := manager.RecreateUnhealthyMeshPods(context.Background(), 2*time.Minute); err != nil {
				t.Fatalf("RecreateUnhealthyMeshPods: %v", err)
			}

			_, err := client.CoreV1().Pods("poddy").Get(context.Background(), tc.name, metav1.GetOptions{})
			if tc.deleted {
				if !apierrors.IsNotFound(err) {
					t.Fatalf("pod still exists, err=%v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("pod was deleted: %v", err)
			}
		})
	}
}

func meshTestPod(name string, ready bool, restarts int32, unreadySince time.Time) *corev1.Pod {
	condition := corev1.PodCondition{
		Type:   corev1.PodReady,
		Status: corev1.ConditionTrue,
	}
	if !ready {
		condition.Status = corev1.ConditionFalse
		condition.LastTransitionTime = metav1.NewTime(unreadySince)
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "poddy",
			Labels: map[string]string{
				labelName:      AppName,
				labelWorkspace: name,
			},
		},
		Status: corev1.PodStatus{
			Conditions: []corev1.PodCondition{condition},
			ContainerStatuses: []corev1.ContainerStatus{{
				Name:         AgentContainerName,
				Ready:        ready,
				RestartCount: restarts,
			}},
		},
	}
}
