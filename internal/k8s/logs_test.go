package k8s

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestContainerState(t *testing.T) {
	started := metav1.NewTime(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	finished := metav1.NewTime(time.Date(2026, 1, 2, 3, 5, 5, 0, time.UTC))

	tests := []struct {
		name       string
		status     corev1.ContainerStatus
		wantState  string
		wantReason string
		wantExit   *int32
	}{
		{
			name:       "waiting",
			status:     corev1.ContainerStatus{State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff", Message: "pulling"}}},
			wantState:  "Waiting",
			wantReason: "ImagePullBackOff",
		},
		{
			name:      "running",
			status:    corev1.ContainerStatus{State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{StartedAt: started}}},
			wantState: "Running",
		},
		{
			name:       "terminated",
			status:     corev1.ContainerStatus{State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{Reason: "Completed", StartedAt: started, FinishedAt: finished, ExitCode: 0}}},
			wantState:  "Terminated",
			wantReason: "Completed",
			wantExit:   int32Ptr(0),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state, reason, _, _, _, exit := containerState(test.status)
			if state != test.wantState || reason != test.wantReason {
				t.Fatalf("state/reason = %q/%q, want %q/%q", state, reason, test.wantState, test.wantReason)
			}
			if test.wantExit == nil && exit != nil {
				t.Fatalf("exit = %v, want nil", *exit)
			}
			if test.wantExit != nil && (exit == nil || *exit != *test.wantExit) {
				t.Fatalf("exit = %v, want %v", exit, *test.wantExit)
			}
		})
	}
}

func TestClampTail(t *testing.T) {
	if got := clampTail(0); got != defaultLogTailLines {
		t.Fatalf("clampTail(0) = %d", got)
	}
	if got := clampTail(maxLogTailLines + 1); got != maxLogTailLines {
		t.Fatalf("clampTail(max+1) = %d", got)
	}
	if got := clampTail(42); got != 42 {
		t.Fatalf("clampTail(42) = %d", got)
	}
}

func TestFindContainerStatus(t *testing.T) {
	statuses := []corev1.ContainerStatus{{Name: "workspace", Ready: true}}
	status, ok := findContainerStatus(statuses, "workspace")
	if !ok || !status.Ready {
		t.Fatalf("findContainerStatus = %#v, %v", status, ok)
	}
	if _, ok := findContainerStatus(statuses, "missing"); ok {
		t.Fatal("unexpected status for missing container")
	}
}

func int32Ptr(value int32) *int32 {
	return &value
}
