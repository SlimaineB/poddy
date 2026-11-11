package k8s

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"poddy/internal/sshkeys"
	"poddy/internal/templates"
)

func newTestManager() *Manager {
	catalog, err := templates.Load("")
	if err != nil {
		panic(err)
	}
	return &Manager{
		Client:          fake.NewClientset(),
		Namespace:       "poddy",
		Templates:       catalog,
		DefaultTemplate: "base",
	}
}

func TestCreateWorkspace(t *testing.T) {
	m := newTestManager()
	ctx := context.Background()

	ws, err := m.Create(ctx, CreateRequest{Name: "app"})
	if err != nil {
		t.Fatal(err)
	}
	if ws.Name != "app" || ws.Template != "base" || ws.Image != "ghcr.io/slimaineb/poddy-workspace-base:latest" {
		t.Fatalf("unexpected workspace: %+v", ws)
	}

	pod, err := m.Client.CoreV1().Pods("poddy").Get(ctx, "poddy-app", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if pod.Labels[labelWorkspace] != "app" || pod.Labels[labelTemplate] != "base" {
		t.Fatalf("labels: %+v", pod.Labels)
	}
	container := pod.Spec.Containers[0]
	if container.Name != ContainerName {
		t.Fatalf("container = %q", container.Name)
	}
	if container.Image != "ghcr.io/slimaineb/poddy-workspace-base:latest" {
		t.Fatalf("image = %q", container.Image)
	}
	if len(container.Command) != 1 || container.Command[0] != "/usr/local/bin/poddy-ssh-start" {
		t.Fatalf("command = %v", container.Command)
	}
	if container.VolumeMounts[0].MountPath != WorkspacePath {
		t.Fatalf("mount path = %q", container.VolumeMounts[0].MountPath)
	}

	pvc, err := m.Client.CoreV1().PersistentVolumeClaims("poddy").Get(ctx, "poddy-app", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("pvc: %v", err)
	}
	if got := pvc.Spec.Resources.Requests.Storage().String(); got != "5Gi" {
		t.Fatalf("storage = %q", got)
	}
}

func TestCreateUsesAgentSidecar(t *testing.T) {
	m := newTestManager()
	m.AgentURL = "http://poddy-agent.poddy.svc:8091"
	m.AgentImage = "ghcr.io/slimaineb/poddy:test"
	m.AgentToken = func(workspace, owner string) (string, error) {
		if workspace != "app" {
			t.Fatalf("workspace = %q", workspace)
		}
		return "agent-token", nil
	}
	ctx := context.Background()

	if _, err := m.Create(ctx, CreateRequest{Name: "app"}); err != nil {
		t.Fatal(err)
	}
	pod, err := m.Get(ctx, "app")
	if err != nil {
		t.Fatal(err)
	}

	workspace := containerByName(pod, ContainerName)
	if workspace == nil {
		t.Fatal("workspace container missing")
	}
	if got := envValue(pod, ContainerName, envAgentToken); got != "" {
		t.Fatalf("workspace agent token = %q, want empty", got)
	}
	if got := envValue(pod, ContainerName, envAgentSidecar); got != "true" {
		t.Fatalf("workspace agent sidecar marker = %q, want true", got)
	}
	if !hasCapability(workspace, "SYS_RESOURCE") {
		t.Fatalf("workspace capabilities = %+v, want SYS_RESOURCE", workspace.SecurityContext.Capabilities)
	}

	agent := containerByName(pod, AgentContainerName)
	if agent == nil {
		t.Fatal("agent sidecar missing")
	}
	if agent.Image != m.AgentImage {
		t.Fatalf("agent image = %q, want %q", agent.Image, m.AgentImage)
	}
	if len(agent.Command) != 1 || agent.Command[0] != "/usr/local/bin/poddy-agent" {
		t.Fatalf("agent command = %v", agent.Command)
	}
	if got := envValue(pod, AgentContainerName, envAgentToken); got != "agent-token" {
		t.Fatalf("agent token = %q", got)
	}
	if got := envValue(pod, AgentContainerName, envWorkspaceName); got != "app" {
		t.Fatalf("agent workspace = %q", got)
	}
	if agent.SecurityContext == nil || agent.SecurityContext.RunAsNonRoot == nil || !*agent.SecurityContext.RunAsNonRoot {
		t.Fatalf("agent security context = %+v", agent.SecurityContext)
	}
	if got := agent.Resources.Limits.Memory().String(); got != "128Mi" {
		t.Fatalf("agent memory limit = %q", got)
	}

	if _, err := m.Stop(ctx, "app"); err != nil {
		t.Fatal(err)
	}
	state, err := m.State(ctx, "app")
	if err != nil {
		t.Fatal(err)
	}
	if !hasContainerSpec(state.Spec, AgentContainerName) {
		t.Fatal("agent sidecar missing from stopped state")
	}
	for _, container := range state.Spec.Containers {
		for _, env := range container.Env {
			if env.Name == envAgentToken && env.Value != "" {
				t.Fatalf("agent token leaked into stopped state: %+v", container)
			}
		}
	}

	if _, err := m.Start(ctx, "app"); err != nil {
		t.Fatal(err)
	}
	restarted, err := m.Get(ctx, "app")
	if err != nil {
		t.Fatal(err)
	}
	if got := envValue(restarted, AgentContainerName, envAgentToken); got != "agent-token" {
		t.Fatalf("restarted agent token = %q", got)
	}
}

func TestCreateFallsBackToInWorkspaceAgent(t *testing.T) {
	m := newTestManager()
	m.AgentURL = "http://poddy-agent.poddy.svc:8091"
	m.AgentToken = func(workspace, owner string) (string, error) {
		return "agent-token", nil
	}
	ctx := context.Background()

	if _, err := m.Create(ctx, CreateRequest{Name: "app"}); err != nil {
		t.Fatal(err)
	}
	pod, err := m.Get(ctx, "app")
	if err != nil {
		t.Fatal(err)
	}
	if got := envValue(pod, ContainerName, envAgentToken); got != "agent-token" {
		t.Fatalf("workspace agent token = %q", got)
	}
	if hasContainer(pod, AgentContainerName) {
		t.Fatal("unexpected agent sidecar")
	}
	if got := envValue(pod, ContainerName, envAgentSidecar); got != "" {
		t.Fatalf("workspace agent sidecar marker = %q, want empty", got)
	}
}

func TestAgentSidecarUsesImageRegistryMirror(t *testing.T) {
	m := newTestManager()
	m.ImageRegistry = "mirror.example.com"
	m.AgentURL = "http://poddy-agent.poddy.svc:8091"
	m.AgentImage = "ghcr.io/slimaineb/poddy:test"
	m.AgentToken = func(workspace, owner string) (string, error) {
		return "agent-token", nil
	}
	ctx := context.Background()

	if _, err := m.Create(ctx, CreateRequest{Name: "app"}); err != nil {
		t.Fatal(err)
	}
	pod, err := m.Get(ctx, "app")
	if err != nil {
		t.Fatal(err)
	}
	agent := containerByName(pod, AgentContainerName)
	if agent == nil {
		t.Fatal("agent sidecar missing")
	}
	if want := "mirror.example.com/slimaineb/poddy:test"; agent.Image != want {
		t.Fatalf("agent image = %q, want %q", agent.Image, want)
	}
}

func hasCapability(container *corev1.Container, want corev1.Capability) bool {
	if container == nil || container.SecurityContext == nil || container.SecurityContext.Capabilities == nil {
		return false
	}
	for _, capability := range container.SecurityContext.Capabilities.Add {
		if capability == want {
			return true
		}
	}
	return false
}

func TestCreateAppliesDefaultStorageClass(t *testing.T) {
	m := newTestManager()
	m.DefaultStorageClass = "gp3"
	ctx := context.Background()

	if _, err := m.Create(ctx, CreateRequest{Name: "app"}); err != nil {
		t.Fatal(err)
	}

	pvc, err := m.Client.CoreV1().PersistentVolumeClaims("poddy").Get(ctx, "poddy-app", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if pvc.Spec.StorageClassName == nil || *pvc.Spec.StorageClassName != "gp3" {
		t.Fatalf("storage class = %v", pvc.Spec.StorageClassName)
	}

	if _, err := m.Stop(ctx, "app"); err != nil {
		t.Fatal(err)
	}
	state, err := m.State(ctx, "app")
	if err != nil {
		t.Fatal(err)
	}
	if state.StorageClass != "gp3" {
		t.Fatalf("state storage class = %q", state.StorageClass)
	}
}

func TestCreateAppliesDefaultStorageClassWithoutTemplateParameter(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"custom.template.yaml": `name: custom
displayName: Custom
image: example/workspace:latest
parameters:
  - name: storage
    default: 5Gi
`,
		"custom.pod.yaml": `apiVersion: v1
kind: Pod
spec:
  containers:
    - name: workspace
      image: example/workspace:latest
      volumeMounts:
        - name: home
          mountPath: /home/dev
  volumes:
    - name: home
      persistentVolumeClaim:
        claimName: "{{ .PVCName }}"
`,
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	catalog, err := templates.Load(dir)
	if err != nil {
		t.Fatal(err)
	}

	m := newTestManager()
	m.Templates = catalog
	m.DefaultTemplate = "custom"
	m.DefaultStorageClass = "gp3"
	ctx := context.Background()

	if _, err := m.Create(ctx, CreateRequest{Name: "app"}); err != nil {
		t.Fatal(err)
	}
	pvc, err := m.Client.CoreV1().PersistentVolumeClaims("poddy").Get(ctx, "poddy-app", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if pvc.Spec.StorageClassName == nil || *pvc.Spec.StorageClassName != "gp3" {
		t.Fatalf("storage class = %v", pvc.Spec.StorageClassName)
	}

	pod, err := m.Client.CoreV1().Pods("poddy").Get(ctx, "poddy-app", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !hasCapability(containerByName(pod, ContainerName), "SYS_RESOURCE") {
		t.Fatal("custom template workspace container did not get SYS_RESOURCE")
	}
}

func TestCreateRewritesImageRegistry(t *testing.T) {
	m := newTestManager()
	m.ImageRegistry = "mirror.example.com"
	m.ImagePullSecrets = []string{"registry-pull"}
	ctx := context.Background()

	ws, err := m.Create(ctx, CreateRequest{Name: "app"})
	if err != nil {
		t.Fatal(err)
	}
	if want := "mirror.example.com/slimaineb/poddy-workspace-base:latest"; ws.Image != want {
		t.Fatalf("workspace image = %q, want %q", ws.Image, want)
	}

	pod, err := m.Client.CoreV1().Pods("poddy").Get(ctx, "poddy-app", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	images := map[string]string{}
	for _, container := range pod.Spec.Containers {
		images[container.Name] = container.Image
	}
	if want := "mirror.example.com/slimaineb/poddy-workspace-base:latest"; images[ContainerName] != want {
		t.Fatalf("workspace container image = %q, want %q", images[ContainerName], want)
	}
	if want := "mirror.example.com/gitpod/openvscode-server:latest"; images["code-server"] != want {
		t.Fatalf("code-server image = %q, want %q", images["code-server"], want)
	}
	if len(pod.Spec.ImagePullSecrets) != 1 || pod.Spec.ImagePullSecrets[0].Name != "registry-pull" {
		t.Fatalf("image pull secrets = %+v", pod.Spec.ImagePullSecrets)
	}
}

func TestCreateRewritesImageRegistryPathOnce(t *testing.T) {
	m := newTestManager()
	m.ImageRegistry = "mirror.example.com/cache"
	ctx := context.Background()

	ws, err := m.Create(ctx, CreateRequest{Name: "app"})
	if err != nil {
		t.Fatal(err)
	}
	if want := "mirror.example.com/cache/slimaineb/poddy-workspace-base:latest"; ws.Image != want {
		t.Fatalf("workspace image = %q, want %q", ws.Image, want)
	}

	pod, err := m.Client.CoreV1().Pods("poddy").Get(ctx, "poddy-app", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	images := map[string]string{}
	for _, container := range pod.Spec.Containers {
		images[container.Name] = container.Image
	}
	if want := "mirror.example.com/cache/slimaineb/poddy-workspace-base:latest"; images[ContainerName] != want {
		t.Fatalf("workspace container image = %q, want %q", images[ContainerName], want)
	}
	if want := "mirror.example.com/cache/gitpod/openvscode-server:latest"; images["code-server"] != want {
		t.Fatalf("code-server image = %q, want %q", images["code-server"], want)
	}
}

func TestCreateRewritesImageRegistryMap(t *testing.T) {
	m := newTestManager()
	m.ImageRegistryMap = map[string]string{
		"ghcr.io":   "mirror.example.com/ghcr",
		"docker.io": "mirror.example.com/docker",
	}
	ctx := context.Background()

	ws, err := m.Create(ctx, CreateRequest{Name: "app"})
	if err != nil {
		t.Fatal(err)
	}
	if want := "mirror.example.com/ghcr/slimaineb/poddy-workspace-base:latest"; ws.Image != want {
		t.Fatalf("workspace image = %q, want %q", ws.Image, want)
	}

	pod, err := m.Client.CoreV1().Pods("poddy").Get(ctx, "poddy-app", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	images := map[string]string{}
	for _, container := range pod.Spec.Containers {
		images[container.Name] = container.Image
	}
	if want := "mirror.example.com/ghcr/slimaineb/poddy-workspace-base:latest"; images[ContainerName] != want {
		t.Fatalf("workspace container image = %q, want %q", images[ContainerName], want)
	}
	if want := "mirror.example.com/docker/gitpod/openvscode-server:latest"; images["code-server"] != want {
		t.Fatalf("code-server image = %q, want %q", images["code-server"], want)
	}
}

func TestCreateWithTemplateParameters(t *testing.T) {
	m := newTestManager()
	ctx := context.Background()

	ws, err := m.Create(ctx, CreateRequest{
		Name:       "gows",
		Template:   "go",
		Parameters: map[string]string{"cpu": "2", "storage": "20Gi"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if ws.Template != "go" {
		t.Fatalf("template = %q", ws.Template)
	}

	pod, err := m.Client.CoreV1().Pods("poddy").Get(ctx, "poddy-gows", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	container := pod.Spec.Containers[0]
	if got := container.Resources.Requests.Cpu().String(); got != "2" {
		t.Fatalf("cpu = %q", got)
	}
	if got := container.Resources.Requests.Memory().String(); got != "2Gi" {
		t.Fatalf("memory = %q", got)
	}
	pvc, err := m.Client.CoreV1().PersistentVolumeClaims("poddy").Get(ctx, "poddy-gows", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := pvc.Spec.Resources.Requests.Storage().String(); got != "20Gi" {
		t.Fatalf("storage = %q", got)
	}
}

func TestCreateRejectsBadInput(t *testing.T) {
	m := newTestManager()
	ctx := context.Background()

	cases := []CreateRequest{
		{Name: ""},
		{Name: "Bad_Name"},
		{Name: "app", CPU: "nope"},
		{Name: "app", Memory: "nope"},
		{Name: "app", Storage: "nope"},
		{Name: "app", Template: "missing"},
		{Name: "app", Parameters: map[string]string{"unknown": "x"}},
	}
	for _, req := range cases {
		if _, err := m.Create(ctx, req); err == nil {
			t.Errorf("expected error for %+v", req)
		}
	}
}

func TestCreateRejectsDuplicate(t *testing.T) {
	m := newTestManager()
	ctx := context.Background()

	if _, err := m.Create(ctx, CreateRequest{Name: "app"}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Create(ctx, CreateRequest{Name: "app"}); err == nil {
		t.Fatal("expected duplicate error")
	}
}

func TestCreateRejectsDuplicateStopped(t *testing.T) {
	m := newTestManager()
	ctx := context.Background()

	if _, err := m.Create(ctx, CreateRequest{Name: "app"}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Stop(ctx, "app"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Create(ctx, CreateRequest{Name: "app"}); err == nil {
		t.Fatal("expected duplicate error for stopped workspace")
	}
	if _, err := m.Get(ctx, "app"); !apierrors.IsNotFound(err) {
		t.Fatalf("stopped workspace was recreated: %v", err)
	}
	if _, err := m.State(ctx, "app"); err != nil {
		t.Fatalf("stopped state was removed: %v", err)
	}
}

func TestDeleteWorkspace(t *testing.T) {
	m := newTestManager()
	ctx := context.Background()

	if _, err := m.Create(ctx, CreateRequest{Name: "app"}); err != nil {
		t.Fatal(err)
	}
	if err := m.Delete(ctx, "app"); err != nil {
		t.Fatal(err)
	}

	if _, err := m.Client.CoreV1().Pods("poddy").Get(ctx, "poddy-app", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("pod still exists: %v", err)
	}
	if _, err := m.Client.CoreV1().PersistentVolumeClaims("poddy").Get(ctx, "poddy-app", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("pvc still exists: %v", err)
	}
}

func TestListWorkspaces(t *testing.T) {
	m := newTestManager()
	ctx := context.Background()

	if _, err := m.Create(ctx, CreateRequest{Name: "one"}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Create(ctx, CreateRequest{Name: "two"}); err != nil {
		t.Fatal(err)
	}

	list, err := m.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("len = %d", len(list))
	}
}

func TestCreateWorkspaceWithSSH(t *testing.T) {
	m := newTestManager()
	m.AuthorizedKeys = []string{"ssh-ed25519 AAAATEST1 poddy", "ssh-ed25519 AAAATEST2 poddy"}
	ctx := context.Background()

	if _, err := m.Create(ctx, CreateRequest{Name: "ssh"}); err != nil {
		t.Fatal(err)
	}
	pod, err := m.Client.CoreV1().Pods("poddy").Get(ctx, "poddy-ssh", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	container := pod.Spec.Containers[0]
	var value string
	for _, env := range container.Env {
		if env.Name == "SSH_AUTHORIZED_KEY" {
			value = env.Value
		}
	}
	if value != "ssh-ed25519 AAAATEST1 poddy\nssh-ed25519 AAAATEST2 poddy" {
		t.Fatalf("env = %q", value)
	}
}

func TestOwnerAndListByOwner(t *testing.T) {
	m := newTestManager()
	ctx := context.Background()

	if _, err := m.Create(ctx, CreateRequest{Name: "alice-ws", Owner: "alice@example.com"}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Create(ctx, CreateRequest{Name: "bob-ws", Owner: "bob@example.com"}); err != nil {
		t.Fatal(err)
	}

	pod, err := m.Get(ctx, "alice-ws")
	if err != nil {
		t.Fatal(err)
	}
	if got := Owner(pod); got != "alice@example.com" {
		t.Fatalf("owner = %q", got)
	}

	list, err := m.ListByOwner(ctx, "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Name != "alice-ws" || list[0].Owner != "alice@example.com" {
		t.Fatalf("owned list = %+v", list)
	}

	all, err := m.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("all list = %+v", all)
	}
}

func TestStopStartRestartWorkspace(t *testing.T) {
	m := newTestManager()
	ctx := context.Background()

	if _, err := m.Create(ctx, CreateRequest{Name: "app", Owner: "alice@example.com"}); err != nil {
		t.Fatal(err)
	}

	stopped, err := m.Stop(ctx, "app")
	if err != nil {
		t.Fatal(err)
	}
	if stopped.Phase != "Stopped" || stopped.Owner != "alice@example.com" || !stopped.Persistent {
		t.Fatalf("stopped workspace = %+v", stopped)
	}
	if _, err := m.Get(ctx, "app"); !apierrors.IsNotFound(err) {
		t.Fatalf("pod still exists: %v", err)
	}
	if _, err := m.Client.CoreV1().PersistentVolumeClaims("poddy").Get(ctx, "poddy-app", metav1.GetOptions{}); err != nil {
		t.Fatalf("pvc removed on stop: %v", err)
	}
	state, err := m.State(ctx, "app")
	if err != nil {
		t.Fatal(err)
	}
	if state.Owner != "alice@example.com" || state.Storage != "5Gi" {
		t.Fatalf("state = %+v", state)
	}

	list, err := m.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Name != "app" || list[0].Phase != "Stopped" {
		t.Fatalf("list = %+v", list)
	}

	if _, err := m.Start(ctx, "app"); err != nil {
		t.Fatal(err)
	}
	pod, err := m.Get(ctx, "app")
	if err != nil {
		t.Fatal(err)
	}
	if pod.Labels[labelWorkspace] != "app" || pod.Labels[labelTemplate] != "base" {
		t.Fatalf("labels = %+v", pod.Labels)
	}
	if _, err := m.State(ctx, "app"); !apierrors.IsNotFound(err) {
		t.Fatalf("state not cleared: %v", err)
	}

	pod.Annotations["test.poddy.io/marker"] = "before"
	if _, err := m.Client.CoreV1().Pods("poddy").Update(ctx, pod, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Restart(ctx, "app"); err != nil {
		t.Fatal(err)
	}
	pod, err = m.Get(ctx, "app")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := pod.Annotations["test.poddy.io/marker"]; ok {
		t.Fatalf("pod was not recreated: %+v", pod.Annotations)
	}

	if _, err := m.Stop(ctx, "app"); err != nil {
		t.Fatal(err)
	}
	if err := m.Delete(ctx, "app"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.State(ctx, "app"); !apierrors.IsNotFound(err) {
		t.Fatalf("state still exists: %v", err)
	}
	if _, err := m.Client.CoreV1().PersistentVolumeClaims("poddy").Get(ctx, "poddy-app", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("pvc still exists: %v", err)
	}
	list, err = m.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("list = %+v", list)
	}
}

func TestStopStartEphemeralWorkspace(t *testing.T) {
	m := newTestManager()
	ctx := context.Background()

	if _, err := m.Create(ctx, CreateRequest{Name: "tmp", Parameters: map[string]string{"storage": ""}}); err != nil {
		t.Fatal(err)
	}
	stopped, err := m.Stop(ctx, "tmp")
	if err != nil {
		t.Fatal(err)
	}
	if stopped.Persistent {
		t.Fatalf("ephemeral workspace marked persistent: %+v", stopped)
	}
	if _, err := m.Start(ctx, "tmp"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Client.CoreV1().PersistentVolumeClaims("poddy").Get(ctx, "poddy-tmp", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("unexpected pvc: %v", err)
	}
}

func TestWorkspaceLifecycleErrors(t *testing.T) {
	m := newTestManager()
	ctx := context.Background()

	if _, err := m.Start(ctx, "missing"); !apierrors.IsNotFound(err) {
		t.Fatalf("start missing = %v", err)
	}
	if _, err := m.Stop(ctx, "missing"); !apierrors.IsNotFound(err) {
		t.Fatalf("stop missing = %v", err)
	}
	if _, err := m.Create(ctx, CreateRequest{Name: "app"}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Start(ctx, "app"); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("start running = %v", err)
	}
	if _, err := m.Stop(ctx, "app"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Stop(ctx, "app"); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("stop stopped = %v", err)
	}
	if _, err := m.Restart(ctx, "app"); err != nil {
		t.Fatalf("restart stopped = %v", err)
	}
}

func testAuthorizedKey(t *testing.T, comment string) string {
	t.Helper()
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ssh.NewPublicKey(public)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key))) + " " + comment
}

func TestCreateWorkspaceUsesOwnerSSHKeys(t *testing.T) {
	m := newTestManager()
	alice := testAuthorizedKey(t, "alice@example.com")
	bob := testAuthorizedKey(t, "bob@example.com")
	admin := testAuthorizedKey(t, "admin")
	keys, err := sshkeys.Parse([]string{alice, bob, admin}, false)
	if err != nil {
		t.Fatal(err)
	}
	m.SSHKeys = keys

	if _, err := m.Create(context.Background(), CreateRequest{Name: "alice-ws", Owner: "alice@example.com"}); err != nil {
		t.Fatal(err)
	}
	pod, err := m.Client.CoreV1().Pods("poddy").Get(context.Background(), "poddy-alice-ws", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var value string
	for _, env := range pod.Spec.Containers[0].Env {
		if env.Name == "SSH_AUTHORIZED_KEY" {
			value = env.Value
		}
	}
	if !strings.Contains(value, alice) || !strings.Contains(value, admin) {
		t.Fatalf("authorized keys = %q", value)
	}
	if strings.Contains(value, bob) {
		t.Fatalf("bob key injected into alice workspace: %q", value)
	}

	secret, err := m.Client.CoreV1().Secrets("poddy").Get(context.Background(), "poddy-alice-ws-ssh-keys", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	authorized := string(secret.Data["authorized_keys"])
	if !strings.Contains(authorized, alice) || !strings.Contains(authorized, admin) || strings.Contains(authorized, bob) {
		t.Fatalf("secret authorized_keys = %q", authorized)
	}
	mounted := false
	for _, mount := range pod.Spec.Containers[0].VolumeMounts {
		if mount.Name == sshKeysVolumeName && mount.MountPath == sshKeysMountPath {
			mounted = true
		}
	}
	if !mounted {
		t.Fatalf("SSH keys Secret is not mounted: %+v", pod.Spec.Containers[0].VolumeMounts)
	}
}

func TestSnapshotRunningAndStopped(t *testing.T) {
	m := newTestManager()
	ctx := context.Background()

	tmpl, err := m.Templates.Get("base")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Create(ctx, CreateRequest{
		Name:       "snap",
		Owner:      "alice@example.com",
		Parameters: map[string]string{"storage": "10Gi"},
	}); err != nil {
		t.Fatal(err)
	}

	running, err := m.Snapshot(ctx, "snap")
	if err != nil {
		t.Fatal(err)
	}
	if running.DesiredState != "running" || running.TemplateVersion != tmpl.Version || running.Parameters["storage"] != "10Gi" {
		t.Fatalf("running snapshot = %+v", running)
	}

	if _, err := m.Stop(ctx, "snap"); err != nil {
		t.Fatal(err)
	}
	stopped, err := m.Snapshot(ctx, "snap")
	if err != nil {
		t.Fatal(err)
	}
	if stopped.DesiredState != "stopped" || stopped.Phase != "Stopped" || stopped.StoppedAt.IsZero() {
		t.Fatalf("stopped snapshot = %+v", stopped)
	}
}

func TestSnapshotTerminatingStopped(t *testing.T) {
	m := newTestManager()
	ctx := context.Background()

	if _, err := m.Create(ctx, CreateRequest{Name: "snap", Owner: "alice@example.com"}); err != nil {
		t.Fatal(err)
	}
	pod, err := m.Get(ctx, "snap")
	if err != nil {
		t.Fatal(err)
	}
	state := m.stateForPod(ctx, pod)
	state.StoppedAt = time.Now().UTC()
	states, err := m.stateStore().Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	states["snap"] = state
	if err := m.stateStore().Save(ctx, states); err != nil {
		t.Fatal(err)
	}

	now := metav1.NewTime(time.Now().UTC())
	pod.DeletionTimestamp = &now
	if _, err := m.Client.CoreV1().Pods(m.Namespace).Update(ctx, pod, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := m.Snapshot(ctx, "snap")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.DesiredState != "stopped" || snapshot.Phase != "Terminating" || snapshot.StoppedAt.IsZero() {
		t.Fatalf("terminating snapshot = %+v", snapshot)
	}
}

func TestPreviewUsage(t *testing.T) {
	m := newTestManager()
	usage, err := m.PreviewUsage(CreateRequest{
		Name:       "preview",
		Template:   "base",
		CPU:        "2",
		Memory:     "4Gi",
		Storage:    "10Gi",
		Parameters: map[string]string{"browserVSCode": "false"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if usage.CPU.Cmp(resource.MustParse("2")) != 0 {
		t.Fatalf("cpu = %s", usage.CPU.String())
	}
	if usage.Memory.Cmp(resource.MustParse("4Gi")) != 0 {
		t.Fatalf("memory = %s", usage.Memory.String())
	}
	if usage.Storage.Cmp(resource.MustParse("10Gi")) != 0 {
		t.Fatalf("storage = %s", usage.Storage.String())
	}
}

func TestPreviewUsageIncludesAgentSidecar(t *testing.T) {
	m := newTestManager()
	m.AgentURL = "http://poddy-agent.poddy.svc:8091"
	m.AgentImage = "ghcr.io/slimaineb/poddy:test"
	m.AgentToken = func(workspace, owner string) (string, error) {
		return "agent-token", nil
	}
	usage, err := m.PreviewUsage(CreateRequest{
		Name:       "preview",
		Parameters: map[string]string{"browserVSCode": "false"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if usage.CPU.Cmp(resource.MustParse("510m")) != 0 {
		t.Fatalf("cpu = %s", usage.CPU.String())
	}
	if usage.Memory.Cmp(resource.MustParse("1040Mi")) != 0 {
		t.Fatalf("memory = %s", usage.Memory.String())
	}
}

func TestCreateQuotaCheckRunsBeforeResources(t *testing.T) {
	m := newTestManager()
	ctx := context.Background()
	called := false

	_, err := m.Create(ctx, CreateRequest{
		Name: "blocked",
		QuotaCheck: func(usage ResourceUsage) error {
			called = true
			if usage.Storage.Cmp(resource.MustParse("5Gi")) != 0 {
				t.Fatalf("storage = %s", usage.Storage.String())
			}
			return errors.New("quota exceeded")
		},
	})
	if err == nil || !strings.Contains(err.Error(), "quota exceeded") {
		t.Fatalf("create error = %v", err)
	}
	if !called {
		t.Fatal("quota check was not called")
	}
	if _, err := m.Client.CoreV1().PersistentVolumeClaims("poddy").Get(ctx, "poddy-blocked", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("pvc should not exist: %v", err)
	}
	if _, err := m.Client.CoreV1().Pods("poddy").Get(ctx, "poddy-blocked", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("pod should not exist: %v", err)
	}
}

func TestListIncludesWorkspaceUsage(t *testing.T) {
	m := newTestManager()
	ctx := context.Background()
	if _, err := m.Create(ctx, CreateRequest{
		Name:       "usage",
		Template:   "base",
		CPU:        "2",
		Memory:     "1Gi",
		Storage:    "7Gi",
		Parameters: map[string]string{"browserVSCode": "false"},
	}); err != nil {
		t.Fatal(err)
	}

	list, err := m.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("list = %+v", list)
	}
	ws := list[0]
	if ws.CPU != "2" || ws.Memory != "1Gi" || ws.Storage != "7Gi" {
		t.Fatalf("usage = cpu %q memory %q storage %q", ws.CPU, ws.Memory, ws.Storage)
	}
}
