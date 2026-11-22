package k8s

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestReconcileAgentsAddsMeshSidecarProbes(t *testing.T) {
	const workspace = "alice"
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: StatefulSetName(workspace), Namespace: "ns"},
		Spec: appsv1.StatefulSetSpec{
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labelsFor(workspace)},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: ContainerName, Image: "workspace"}},
				},
			},
		},
	}
	m := &Manager{
		Client:         fake.NewSimpleClientset(sts),
		Namespace:      "ns",
		AgentImage:     "agent",
		MeshControlURL: "https://mesh.example.com",
		MeshAuthKey:    "tskey-auth-test",
	}
	if err := m.ReconcileAgents(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := m.Client.AppsV1().StatefulSets("ns").Get(context.Background(), StatefulSetName(workspace), metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var agent *corev1.Container
	for i := range got.Spec.Template.Spec.Containers {
		if got.Spec.Template.Spec.Containers[i].Name == AgentContainerName {
			agent = &got.Spec.Template.Spec.Containers[i]
		}
	}
	if agent == nil {
		t.Fatal("agent sidecar not added")
	}
	if agent.StartupProbe == nil || agent.StartupProbe.HTTPGet == nil || agent.StartupProbe.HTTPGet.Path != "/readyz" {
		t.Fatalf("startup probe = %#v", agent.StartupProbe)
	}
	if agent.ReadinessProbe == nil || agent.ReadinessProbe.HTTPGet == nil || agent.ReadinessProbe.HTTPGet.Path != "/readyz" {
		t.Fatalf("readiness probe = %#v", agent.ReadinessProbe)
	}
	if agent.LivenessProbe == nil || agent.LivenessProbe.HTTPGet == nil || agent.LivenessProbe.HTTPGet.Path != "/healthz" {
		t.Fatalf("liveness probe = %#v", agent.LivenessProbe)
	}
}
