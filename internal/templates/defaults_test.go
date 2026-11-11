package templates

import (
	"strings"
	"testing"
)

func testData() Data {
	return Data{
		Name:           "app",
		PVCName:        "poddy-app",
		Image:          "example/workspace:latest",
		Parameters:     map[string]string{"cpu": "500m", "memory": "1Gi", "storage": "5Gi", "browserVSCode": "true"},
		Storage:        "5Gi",
		AuthorizedKeys: "ssh-ed25519 AAAA test",
	}
}

func TestApplyDefaultsInjectsWorkspace(t *testing.T) {
	pod, err := ApplyDefaults([]byte("apiVersion: v1\nkind: Pod\nspec: {}\n"), testData())
	if err != nil {
		t.Fatal(err)
	}

	if pod.Spec.AutomountServiceAccountToken == nil || *pod.Spec.AutomountServiceAccountToken {
		t.Fatal("automountServiceAccountToken was not disabled")
	}
	if pod.Spec.SecurityContext == nil || pod.Spec.SecurityContext.FSGroup == nil || *pod.Spec.SecurityContext.FSGroup != 1000 {
		t.Fatalf("security context = %+v", pod.Spec.SecurityContext)
	}
	if len(pod.Spec.Containers) != 2 {
		t.Fatalf("containers = %+v", pod.Spec.Containers)
	}

	workspace := pod.Spec.Containers[0]
	if workspace.Name != "workspace" || workspace.Image != "example/workspace:latest" {
		t.Fatalf("workspace = %+v", workspace)
	}
	if len(workspace.Command) != 1 || workspace.Command[0] != SSHCommand {
		t.Fatalf("command = %v", workspace.Command)
	}
	if workspace.WorkingDir != WorkspacePath {
		t.Fatalf("workingDir = %q", workspace.WorkingDir)
	}
	if !hasEnv(workspace.Env, "SSH_AUTHORIZED_KEY") {
		t.Fatalf("env = %+v", workspace.Env)
	}
	if !hasPort(workspace.Ports, 22) {
		t.Fatalf("ports = %+v", workspace.Ports)
	}
	if got := workspace.Resources.Requests.Cpu().String(); got != "500m" {
		t.Fatalf("cpu = %q", got)
	}
	if got := workspace.Resources.Requests.Memory().String(); got != "1Gi" {
		t.Fatalf("memory = %q", got)
	}
	if !hasVolumeMount(workspace.VolumeMounts, "workspace") {
		t.Fatalf("volumeMounts = %+v", workspace.VolumeMounts)
	}
	for _, mount := range workspace.VolumeMounts {
		if mount.Name == "workspace" && mount.MountPath != "/home/dev" {
			t.Fatalf("workspace mountPath = %q", mount.MountPath)
		}
	}

	if len(pod.Spec.Volumes) != 1 || pod.Spec.Volumes[0].PersistentVolumeClaim == nil || pod.Spec.Volumes[0].PersistentVolumeClaim.ClaimName != "poddy-app" {
		t.Fatalf("volumes = %+v", pod.Spec.Volumes)
	}

	code := pod.Spec.Containers[1]
	if code.Name != "code-server" || code.Image != "gitpod/openvscode-server:latest" {
		t.Fatalf("code-server = %+v", code)
	}
	if code.WorkingDir != WorkspacePath {
		t.Fatalf("code-server workingDir = %q", code.WorkingDir)
	}
	if len(code.Args) != 1 || code.Args[0] == "" {
		t.Fatalf("code-server args = %+v", code.Args)
	}
	if !strings.Contains(code.Args[0], "--default-folder /home/dev") {
		t.Fatalf("code-server default folder args = %+v", code.Args)
	}
}

func TestApplyDefaultsInjectsJupyterLab(t *testing.T) {
	data := testData()
	data.Parameters["jupyterLab"] = "true"

	pod, err := ApplyDefaults([]byte("apiVersion: v1\nkind: Pod\nspec: {}\n"), data)
	if err != nil {
		t.Fatal(err)
	}
	if len(pod.Spec.Containers) != 3 {
		t.Fatalf("containers = %+v", pod.Spec.Containers)
	}
	jupyter := containerByName(pod.Spec.Containers, "jupyterlab")
	if jupyter == nil {
		t.Fatalf("jupyterlab container missing: %+v", pod.Spec.Containers)
	}
	if jupyter.Image != "quay.io/jupyter/base-notebook:latest" {
		t.Fatalf("jupyterlab image = %q", jupyter.Image)
	}
	if jupyter.WorkingDir != WorkspacePath {
		t.Fatalf("jupyterlab workingDir = %q", jupyter.WorkingDir)
	}
	if !hasVolumeMount(jupyter.VolumeMounts, "workspace") {
		t.Fatalf("jupyterlab volumeMounts = %+v", jupyter.VolumeMounts)
	}
	args := strings.Join(jupyter.Args, " ")
	if !strings.Contains(args, "--ServerApp.base_url=/api/workspaces/app/lab/") {
		t.Fatalf("jupyterlab args = %+v", jupyter.Args)
	}
	if !strings.Contains(args, "--ServerApp.root_dir=/home/dev") {
		t.Fatalf("jupyterlab args = %+v", jupyter.Args)
	}
}

func TestApplyDefaultsEphemeral(t *testing.T) {
	data := testData()
	data.Storage = ""
	data.Parameters["storage"] = ""
	data.Parameters["browserVSCode"] = "false"

	pod, err := ApplyDefaults([]byte("apiVersion: v1\nkind: Pod\nspec: {}\n"), data)
	if err != nil {
		t.Fatal(err)
	}
	if len(pod.Spec.Volumes) != 1 || pod.Spec.Volumes[0].EmptyDir == nil {
		t.Fatalf("volumes = %+v", pod.Spec.Volumes)
	}
	if len(pod.Spec.Containers) != 1 {
		t.Fatalf("containers = %+v", pod.Spec.Containers)
	}
}

func TestApplyDefaultsPreservesExplicitValues(t *testing.T) {
	raw := []byte(`
apiVersion: v1
kind: Pod
spec:
  automountServiceAccountToken: true
  securityContext:
    fsGroup: 2000
  containers:
    - name: workspace
      image: custom/image:1
      imagePullPolicy: Always
      command: ["/custom/start"]
      workingDir: /src
      env:
        - name: SSH_AUTHORIZED_KEY
          value: custom
      ports:
        - name: custom-ssh
          containerPort: 2222
      resources:
        requests:
          cpu: "2"
          memory: 4Gi
      volumeMounts:
        - name: workspace
          mountPath: /src
    - name: code-server
      image: custom/code-server:1
  volumes:
    - name: workspace
      emptyDir: {}
`)
	pod, err := ApplyDefaults(raw, testData())
	if err != nil {
		t.Fatal(err)
	}

	if pod.Spec.AutomountServiceAccountToken == nil || !*pod.Spec.AutomountServiceAccountToken {
		t.Fatal("automountServiceAccountToken was overridden")
	}
	if *pod.Spec.SecurityContext.FSGroup != 2000 {
		t.Fatalf("fsGroup = %d", *pod.Spec.SecurityContext.FSGroup)
	}
	workspace := pod.Spec.Containers[0]
	if workspace.Image != "custom/image:1" || workspace.ImagePullPolicy != "Always" {
		t.Fatalf("workspace = %+v", workspace)
	}
	if len(workspace.Command) != 1 || workspace.Command[0] != "/custom/start" {
		t.Fatalf("command = %v", workspace.Command)
	}
	if workspace.WorkingDir != "/src" {
		t.Fatalf("workingDir = %q", workspace.WorkingDir)
	}
	if workspace.Env[0].Value != "custom" {
		t.Fatalf("env = %+v", workspace.Env)
	}
	if len(workspace.Ports) != 1 || workspace.Ports[0].ContainerPort != 2222 {
		t.Fatalf("ports = %+v", workspace.Ports)
	}
	if got := workspace.Resources.Requests.Cpu().String(); got != "2" {
		t.Fatalf("cpu = %q", got)
	}
	if len(workspace.VolumeMounts) != 1 || workspace.VolumeMounts[0].MountPath != "/src" {
		t.Fatalf("volumeMounts = %+v", workspace.VolumeMounts)
	}
	if len(pod.Spec.Volumes) != 1 || pod.Spec.Volumes[0].EmptyDir == nil {
		t.Fatalf("volumes = %+v", pod.Spec.Volumes)
	}
	if pod.Spec.Containers[1].Image != "custom/code-server:1" {
		t.Fatalf("code-server = %+v", pod.Spec.Containers[1])
	}
}

func TestApplyDefaultsMergesContainerFields(t *testing.T) {
	raw := []byte(`
apiVersion: v1
kind: Pod
spec:
  containers:
    - name: workspace
      env:
        - name: GOPATH
          value: /home/dev/go
`)
	pod, err := ApplyDefaults(raw, testData())
	if err != nil {
		t.Fatal(err)
	}
	workspace := pod.Spec.Containers[0]
	if workspace.Image != "example/workspace:latest" {
		t.Fatalf("image = %q", workspace.Image)
	}
	if !hasEnv(workspace.Env, "SSH_AUTHORIZED_KEY") || !hasEnv(workspace.Env, "GOPATH") {
		t.Fatalf("env = %+v", workspace.Env)
	}
	if !hasVolumeMount(workspace.VolumeMounts, "workspace") {
		t.Fatalf("volumeMounts = %+v", workspace.VolumeMounts)
	}
}

func TestApplyDefaultsAllowsAdditionalVolumesAndEnv(t *testing.T) {
	raw := []byte(`
apiVersion: v1
kind: Pod
spec:
  containers:
    - name: workspace
      env:
        - name: NODE_ENV
          value: development
        - name: EXTRA
          value: "1"
      volumeMounts:
        - name: data-a
          mountPath: /data/a
        - name: data-b
          mountPath: /data/b
        - name: cache
          mountPath: /cache
  volumes:
    - name: data-a
      persistentVolumeClaim:
        claimName: data-a
    - name: data-b
      persistentVolumeClaim:
        claimName: data-b
    - name: cache
      emptyDir: {}
`)
	pod, err := ApplyDefaults(raw, testData())
	if err != nil {
		t.Fatal(err)
	}
	workspace := pod.Spec.Containers[0]
	if !hasEnv(workspace.Env, "NODE_ENV") || !hasEnv(workspace.Env, "EXTRA") || !hasEnv(workspace.Env, "SSH_AUTHORIZED_KEY") {
		t.Fatalf("env = %+v", workspace.Env)
	}
	for _, name := range []string{"data-a", "data-b", "cache", "workspace"} {
		if !hasVolumeMount(workspace.VolumeMounts, name) {
			t.Fatalf("missing volume mount %q: %+v", name, workspace.VolumeMounts)
		}
	}
	if len(pod.Spec.Volumes) != 4 {
		t.Fatalf("volumes = %+v", pod.Spec.Volumes)
	}
}

func TestApplyDefaultsRejectsInvalidResources(t *testing.T) {
	data := testData()
	data.Parameters["cpu"] = "nope"
	if _, err := ApplyDefaults([]byte("apiVersion: v1\nkind: Pod\nspec: {}\n"), data); err == nil {
		t.Fatal("expected invalid cpu error")
	}
}

func TestApplyDefaultsRequiresWorkspaceImage(t *testing.T) {
	data := testData()
	data.Image = ""
	if _, err := ApplyDefaults([]byte("apiVersion: v1\nkind: Pod\nspec: {}\n"), data); err == nil {
		t.Fatal("expected missing image error")
	}
}

func TestApplyDefaultsWithoutResourceParameters(t *testing.T) {
	data := testData()
	delete(data.Parameters, "cpu")
	delete(data.Parameters, "memory")
	pod, err := ApplyDefaults([]byte("apiVersion: v1\nkind: Pod\nspec: {}\n"), data)
	if err != nil {
		t.Fatal(err)
	}
	workspace := pod.Spec.Containers[0]
	if len(workspace.Resources.Requests) != 0 {
		t.Fatalf("requests = %+v", workspace.Resources.Requests)
	}
}
