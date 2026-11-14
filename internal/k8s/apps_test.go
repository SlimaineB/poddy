package k8s

import (
	"context"
	"reflect"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestNormalizeApps(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{name: "nil", in: nil, want: nil},
		{name: "empty", in: []string{}, want: []string{}},
		{name: "valid", in: []string{"vscode", "jupyterlab"}, want: []string{"vscode", "jupyterlab"}},
		{name: "normalizes", in: []string{" VSCODE ", "JupyterLab"}, want: []string{"vscode", "jupyterlab"}},
		{name: "dedupes", in: []string{"vscode", "vscode"}, want: []string{"vscode"}},
		{name: "drops unknown", in: []string{"vscode", "unknown", ""}, want: []string{"vscode"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := NormalizeApps(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("NormalizeApps(%v) = %#v, want %#v", tc.in, got, tc.want)
			}
		})
	}
}

func TestAppsFromAnnotations(t *testing.T) {
	cases := []struct {
		name        string
		annotations map[string]string
		want        []string
	}{
		{name: "missing", annotations: nil, want: nil},
		{name: "empty", annotations: map[string]string{annotationApps: ""}, want: []string{}},
		{name: "list", annotations: map[string]string{annotationApps: "vscode,jupyterlab"}, want: []string{"vscode", "jupyterlab"}},
		{name: "invalid", annotations: map[string]string{annotationApps: "vscode,unknown"}, want: []string{"vscode"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := appsFromAnnotations(tc.annotations)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("appsFromAnnotations(%v) = %#v, want %#v", tc.annotations, got, tc.want)
			}
		})
	}
}

func TestCreateWorkspaceApps(t *testing.T) {
	m := newTestManager()
	ctx := context.Background()

	ws, err := m.Create(ctx, CreateRequest{
		Name:     "app",
		Template: "python",
		Apps:     []string{"vscode", "jupyterlab"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if ws.BrowserVSCode {
		t.Fatal("browser VS Code should be disabled")
	}
	if !ws.JupyterLab {
		t.Fatal("JupyterLab should be enabled")
	}
	if !reflect.DeepEqual(ws.Apps, []string{"vscode", "jupyterlab"}) {
		t.Fatalf("apps = %#v", ws.Apps)
	}

	pod, err := m.Client.CoreV1().Pods("poddy").Get(ctx, PodName("app"), metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := pod.Annotations[annotationApps]; got != "vscode,jupyterlab" {
		t.Fatalf("apps annotation = %q", got)
	}
}

func TestCreateWorkspaceNoApps(t *testing.T) {
	m := newTestManager()
	ctx := context.Background()

	ws, err := m.Create(ctx, CreateRequest{
		Name:     "app",
		Template: "python",
		Apps:     []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if ws.BrowserVSCode || ws.JupyterLab {
		t.Fatalf("sidecars should be disabled: browser=%v jupyter=%v", ws.BrowserVSCode, ws.JupyterLab)
	}
	if ws.Apps == nil || len(ws.Apps) != 0 {
		t.Fatalf("apps = %#v, want empty non-nil", ws.Apps)
	}

	pod, err := m.Client.CoreV1().Pods("poddy").Get(ctx, PodName("app"), metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := pod.Annotations[annotationApps]; !ok {
		t.Fatal("apps annotation should be present")
	}
}

func TestCreateWorkspaceLegacyApps(t *testing.T) {
	m := newTestManager()
	ctx := context.Background()

	ws, err := m.Create(ctx, CreateRequest{Name: "app"})
	if err != nil {
		t.Fatal(err)
	}
	if ws.Apps != nil {
		t.Fatalf("apps = %#v, want nil", ws.Apps)
	}
	if !ws.BrowserVSCode {
		t.Fatal("template default should enable browser VS Code")
	}
}
