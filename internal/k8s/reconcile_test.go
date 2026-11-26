package k8s

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
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

func TestReconcileAgentsMovesMeshAgentIntoWorkspace(t *testing.T) {
	const workspace = "alice"
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: StatefulSetName(workspace), Namespace: "ns"},
		Spec: appsv1.StatefulSetSpec{
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labelsFor(workspace)},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: ContainerName, Image: "workspace"},
						{Name: AgentContainerName, Image: "agent"},
					},
				},
			},
		},
	}
	m := &Manager{
		Client:         fake.NewSimpleClientset(sts),
		Namespace:      "ns",
		MeshControlURL: "https://mesh.example.com",
		MeshAuthKey:    "tskey-auth-test",
		MeshCACert:     "ca-cert",
	}
	if err := m.ReconcileAgents(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := m.Client.AppsV1().StatefulSets("ns").Get(context.Background(), StatefulSetName(workspace), metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if hasContainerSpec(got.Spec.Template.Spec, AgentContainerName) {
		t.Fatal("agent sidecar still present")
	}
	pod := &corev1.Pod{Spec: got.Spec.Template.Spec}
	for name, want := range map[string]string{
		envMeshControlURL: "https://mesh.example.com",
		envMeshAuthKey:    "tskey-auth-test",
		envMeshCACert:     "ca-cert",
		envMeshStateDir:   agentStateMountPath + "/tsnet",
	} {
		if got := envValue(pod, ContainerName, name); got != want {
			t.Fatalf("%s = %q, want %q", name, got, want)
		}
	}
	ws := containerByName(pod, ContainerName)
	if ws == nil {
		t.Fatal("workspace container missing")
	}
	if ws.ReadinessProbe == nil || ws.ReadinessProbe.HTTPGet == nil || ws.ReadinessProbe.HTTPGet.Path != "/readyz" {
		t.Fatalf("readiness probe = %#v", ws.ReadinessProbe)
	}
}

func TestReconcileAgentsRemovesMeshAgentWhenDisabled(t *testing.T) {
	const workspace = "alice"
	probe := &corev1.Probe{
		ProbeHandler: corev1.ProbeHandler{
			HTTPGet: &corev1.HTTPGetAction{Path: "/readyz", Port: intstr.FromInt(8081)},
		},
	}
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: StatefulSetName(workspace), Namespace: "ns"},
		Spec: appsv1.StatefulSetSpec{
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labelsFor(workspace)},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  ContainerName,
							Image: "workspace",
							Env: []corev1.EnvVar{
								{Name: envMeshControlURL, Value: "https://mesh.example.com"},
								{Name: envMeshAuthKey, Value: "tskey-auth-test"},
								{Name: envMeshCACert, Value: "ca-cert"},
								{Name: envMeshStateDir, Value: agentStateMountPath + "/tsnet"},
							},
							StartupProbe:   probe,
							ReadinessProbe: probe,
						},
						{Name: AgentContainerName, Image: "agent"},
					},
				},
			},
		},
	}
	m := &Manager{Client: fake.NewSimpleClientset(sts), Namespace: "ns"}
	if err := m.ReconcileAgents(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := m.Client.AppsV1().StatefulSets("ns").Get(context.Background(), StatefulSetName(workspace), metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if hasContainerSpec(got.Spec.Template.Spec, AgentContainerName) {
		t.Fatal("agent sidecar still present")
	}
	pod := &corev1.Pod{Spec: got.Spec.Template.Spec}
	for _, name := range []string{envMeshControlURL, envMeshAuthKey, envMeshCACert, envMeshStateDir} {
		if got := envValue(pod, ContainerName, name); got != "" {
			t.Fatalf("%s = %q, want empty", name, got)
		}
	}
	ws := containerByName(pod, ContainerName)
	if ws == nil {
		t.Fatal("workspace container missing")
	}
	if ws.StartupProbe != nil || ws.ReadinessProbe != nil {
		t.Fatalf("mesh probes left behind: startup=%#v readiness=%#v", ws.StartupProbe, ws.ReadinessProbe)
	}
}
