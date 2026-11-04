package templates

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadEmbedded(t *testing.T) {
	catalog, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	list := catalog.List()
	if len(list) < 2 {
		t.Fatalf("templates = %+v", list)
	}
	if _, err := catalog.Get("base"); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.Get("go"); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.Get("missing"); err == nil {
		t.Fatal("expected missing template error")
	}
}

func TestResolve(t *testing.T) {
	catalog, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	tmpl, err := catalog.Get("base")
	if err != nil {
		t.Fatal(err)
	}

	params, err := tmpl.Resolve(map[string]string{"cpu": "2"})
	if err != nil {
		t.Fatal(err)
	}
	if params["cpu"] != "2" || params["memory"] != "1Gi" || params["storage"] != "5Gi" {
		t.Fatalf("params = %+v", params)
	}

	if _, err := tmpl.Resolve(map[string]string{"unknown": "x"}); err == nil {
		t.Fatal("expected unknown parameter error")
	}
}

func TestLinks(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("linked.template.yaml", "name: linked\nimage: example:1\nlinks:\n  - name: Docs\n    url: https://example.com/{name}\n    icon: external\n")
	write("linked.pod.yaml", "apiVersion: v1\nkind: Pod\nspec:\n  containers:\n    - name: workspace\n      image: example:1\n")
	catalog, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	tmpl, err := catalog.Get("linked")
	if err != nil {
		t.Fatal(err)
	}
	if len(tmpl.Links) != 1 || tmpl.Links[0].Icon != "external" {
		t.Fatalf("links = %+v", tmpl.Links)
	}
	list := catalog.List()
	found := false
	for _, info := range list {
		if info.Name == "linked" && len(info.Links) == 1 {
			found = true
		}
	}
	if !found {
		t.Fatalf("catalog list has no links: %+v", list)
	}
}

func TestRender(t *testing.T) {
	catalog, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	tmpl, err := catalog.Get("base")
	if err != nil {
		t.Fatal(err)
	}
	params, err := tmpl.Resolve(nil)
	if err != nil {
		t.Fatal(err)
	}

	pod, err := tmpl.Render(Data{
		Name:           "app",
		Namespace:      "poddy",
		PodName:        "poddy-app",
		PVCName:        "poddy-app",
		Parameters:     params,
		AuthorizedKeys: "ssh-ed25519 AAAA test",
	})
	if err != nil {
		t.Fatal(err)
	}
	container := pod.Spec.Containers[0]
	if container.Image != "ghcr.io/slimaineb/poddy-workspace-base:latest" {
		t.Fatalf("image = %q", container.Image)
	}
	if len(container.Command) != 1 || container.Command[0] != "/usr/local/bin/poddy-ssh-start" {
		t.Fatalf("command = %v", container.Command)
	}
	if container.Env[0].Value != "ssh-ed25519 AAAA test" {
		t.Fatalf("env = %+v", container.Env)
	}
	if pod.Spec.Volumes[0].PersistentVolumeClaim == nil || pod.Spec.Volumes[0].PersistentVolumeClaim.ClaimName != "poddy-app" {
		t.Fatalf("volumes = %+v", pod.Spec.Volumes)
	}
}

func TestRenderEphemeral(t *testing.T) {
	catalog, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	tmpl, err := catalog.Get("base")
	if err != nil {
		t.Fatal(err)
	}
	params, err := tmpl.Resolve(map[string]string{"storage": ""})
	if err != nil {
		t.Fatal(err)
	}

	pod, err := tmpl.Render(Data{Parameters: params})
	if err != nil {
		t.Fatal(err)
	}
	if pod.Spec.Volumes[0].EmptyDir == nil {
		t.Fatalf("volumes = %+v", pod.Spec.Volumes)
	}
}

func writeFlatTemplate(t *testing.T, dir, name, displayName string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name+".template.yaml"), []byte("name: "+name+"\ndisplayName: "+displayName+"\nimage: example/image:latest\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".pod.yaml"), []byte("apiVersion: v1\nkind: Pod\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadFlatTemplates(t *testing.T) {
	dir := t.TempDir()
	writeFlatTemplate(t, dir, "node", "Node.js")

	catalog, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	tmpl, err := catalog.Get("node")
	if err != nil {
		t.Fatal(err)
	}
	if tmpl.DisplayName != "Node.js" {
		t.Fatalf("display name = %q", tmpl.DisplayName)
	}
	if _, err := catalog.Get("base"); err != nil {
		t.Fatalf("embedded template missing after flat load: %v", err)
	}
}

func TestReload(t *testing.T) {
	dir := t.TempDir()
	writeFlatTemplate(t, dir, "node", "Node.js")

	catalog, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	writeFlatTemplate(t, dir, "python", "Python")
	if err := catalog.Reload(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.Get("python"); err != nil {
		t.Fatal(err)
	}
	if tmpl, err := catalog.Get("node"); err != nil || tmpl.DisplayName != "Node.js" {
		t.Fatalf("existing template after reload = %+v, %v", tmpl, err)
	}
}

func TestLoadSources(t *testing.T) {
	catalog, err := LoadSources([]Source{{
		Name:     "node",
		Version:  "2.0.0",
		Template: []byte("name: node\ndisplayName: Node.js\nversion: 1.0.0\nimage: example/image:latest\n"),
		Pod:      []byte("apiVersion: v1\nkind: Pod\n"),
	}})
	if err != nil {
		t.Fatal(err)
	}
	tmpl, err := catalog.Get("node")
	if err != nil {
		t.Fatal(err)
	}
	if tmpl.Version != "2.0.0" {
		t.Fatalf("source version did not override template.yaml: %q", tmpl.Version)
	}
	if _, err := catalog.Get("base"); err != nil {
		t.Fatalf("embedded template missing: %v", err)
	}

	_, err = LoadSources([]Source{{
		Name:     "broken",
		Template: []byte("name: other\nimage: example/image:latest\n"),
		Pod:      []byte("apiVersion: v1\nkind: Pod\n"),
	}})
	if err == nil {
		t.Fatal("expected name mismatch error")
	}
}
