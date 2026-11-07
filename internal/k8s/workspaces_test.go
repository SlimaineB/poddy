package k8s

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
