package k8s

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"poddy/internal/templates"
)

func TestReloadTemplatesFromConfigMaps(t *testing.T) {
	catalog, err := templates.Load("")
	if err != nil {
		t.Fatal(err)
	}
	client := fake.NewSimpleClientset(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "node-template",
			Namespace: "poddy",
			Labels: map[string]string{
				TemplateLabel:        "true",
				TemplateNameLabel:    "node",
				TemplateVersionLabel: "1.0.0",
			},
		},
		Data: map[string]string{
			"template.yaml": "name: node\ndisplayName: Node.js\nimage: example/image:latest\n",
			"pod.yaml":      "apiVersion: v1\nkind: Pod\n",
		},
	})
	if err := reloadTemplates(context.Background(), client, "poddy", TemplateLabel+"=true", catalog); err != nil {
		t.Fatal(err)
	}
	tmpl, err := catalog.Get("node")
	if err != nil {
		t.Fatal(err)
	}
	if tmpl.DisplayName != "Node.js" {
		t.Fatalf("display name = %q", tmpl.DisplayName)
	}
	if tmpl.Version != "1.0.0" {
		t.Fatalf("version = %q", tmpl.Version)
	}
}

func templateConfigMap(name, templateName, version string, active bool) corev1.ConfigMap {
	labels := map[string]string{
		TemplateLabel:        "true",
		TemplateNameLabel:    templateName,
		TemplateVersionLabel: version,
	}
	if active {
		labels[TemplateActiveLabel] = "true"
	}
	return corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
		Data: map[string]string{
			"template.yaml": "name: " + templateName + "\nimage: example/image:" + version + "\n",
			"pod.yaml":      "apiVersion: v1\nkind: Pod\n",
		},
	}
}

func TestTemplateSourcesSelectActiveVersion(t *testing.T) {
	configMaps := []corev1.ConfigMap{
		templateConfigMap("node-1", "node", "1.0.0", false),
		templateConfigMap("node-2", "node", "2.0.0", true),
		templateConfigMap("go-1", "go", "1.0.0", false),
	}
	sources, err := templateSources(configMaps)
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 2 {
		t.Fatalf("sources = %+v", sources)
	}
	if sources[0].Name != "go" || sources[0].Version != "1.0.0" {
		t.Fatalf("go source = %+v", sources[0])
	}
	if sources[1].Name != "node" || sources[1].Version != "2.0.0" {
		t.Fatalf("node source = %+v", sources[1])
	}
}

func TestTemplateSourcesRejectsAmbiguousVersions(t *testing.T) {
	tests := []struct {
		name       string
		configMaps []corev1.ConfigMap
	}{
		{
			name: "multiple active",
			configMaps: []corev1.ConfigMap{
				templateConfigMap("node-1", "node", "1.0.0", true),
				templateConfigMap("node-2", "node", "2.0.0", true),
			},
		},
		{
			name: "none active",
			configMaps: []corev1.ConfigMap{
				templateConfigMap("node-1", "node", "1.0.0", false),
				templateConfigMap("node-2", "node", "2.0.0", false),
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := templateSources(test.configMaps); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}
