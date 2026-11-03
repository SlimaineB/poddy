package k8s

import (
	"context"
	"fmt"
	"log"
	"sort"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"

	"poddy/internal/templates"
)

const (
	// TemplateLabel marks ConfigMaps that the portal should load as templates.
	TemplateLabel = "poddy.io/template"
	// TemplateNameLabel overrides the template name. Defaults to the ConfigMap name.
	TemplateNameLabel = "poddy.io/template-name"
	// TemplateVersionLabel identifies the version stored in a ConfigMap.
	TemplateVersionLabel = "poddy.io/template-version"
	// TemplateActiveLabel marks the version the portal should serve. Exactly
	// one version per template must be active when several are present.
	TemplateActiveLabel = "poddy.io/template-active"
)

// WatchTemplates watches labeled ConfigMaps in namespace and reloads the
// catalog without restarting the portal. A bad update keeps the previous
// catalog active; fix the ConfigMap and the next event retries the reload.
func WatchTemplates(ctx context.Context, client kubernetes.Interface, namespace string, catalog *templates.Catalog) {
	selector := TemplateLabel + "=true"
	for ctx.Err() == nil {
		watcher, err := client.CoreV1().ConfigMaps(namespace).Watch(ctx, metav1.ListOptions{LabelSelector: selector})
		if err != nil {
			log.Printf("templates: watch ConfigMaps in %s: %v", namespace, err)
			if !sleep(ctx, 2*time.Second) {
				return
			}
			continue
		}
		if err := reloadTemplates(ctx, client, namespace, selector, catalog); err != nil {
			log.Printf("templates: reload from ConfigMaps failed, keeping previous catalog: %v", err)
		}
		for event := range watcher.ResultChan() {
			if ctx.Err() != nil {
				break
			}
			switch event.Type {
			case watch.Added, watch.Modified, watch.Deleted:
				if err := reloadTemplates(ctx, client, namespace, selector, catalog); err != nil {
					log.Printf("templates: reload from ConfigMaps failed, keeping previous catalog: %v", err)
				}
			}
		}
		watcher.Stop()
		if ctx.Err() != nil {
			return
		}
		if !sleep(ctx, 2*time.Second) {
			return
		}
	}
}

func reloadTemplates(ctx context.Context, client kubernetes.Interface, namespace, selector string, catalog *templates.Catalog) error {
	list, err := client.CoreV1().ConfigMaps(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return err
	}
	sources, err := templateSources(list.Items)
	if err != nil {
		return err
	}
	if err := catalog.ReloadSources(sources); err != nil {
		return err
	}
	log.Printf("templates: loaded %d templates from %d ConfigMaps", len(catalog.List()), len(list.Items))
	return nil
}

// templateSources groups versioned ConfigMaps by template name and selects the
// active version. Unversioned ConfigMaps remain valid as long as there is only
// one version for that template.
func templateSources(configMaps []corev1.ConfigMap) ([]templates.Source, error) {
	type candidate struct {
		source templates.Source
		active bool
	}
	groups := make(map[string][]candidate)
	for _, configMap := range configMaps {
		templateYAML, ok := configMap.Data["template.yaml"]
		if !ok {
			return nil, fmt.Errorf("ConfigMap %s: template.yaml is required", configMap.Name)
		}
		podYAML, ok := configMap.Data["pod.yaml"]
		if !ok {
			return nil, fmt.Errorf("ConfigMap %s: pod.yaml is required", configMap.Name)
		}
		name := configMap.Labels[TemplateNameLabel]
		if name == "" {
			name = configMap.Name
		}
		groups[name] = append(groups[name], candidate{
			source: templates.Source{
				Name:     name,
				Version:  configMap.Labels[TemplateVersionLabel],
				Template: []byte(templateYAML),
				Pod:      []byte(podYAML),
			},
			active: configMap.Labels[TemplateActiveLabel] == "true",
		})
	}

	names := make([]string, 0, len(groups))
	for name := range groups {
		names = append(names, name)
	}
	sort.Strings(names)

	sources := make([]templates.Source, 0, len(names))
	for _, name := range names {
		candidates := groups[name]
		active := 0
		selected := 0
		for i := range candidates {
			if candidates[i].active {
				active++
				selected = i
			}
		}
		switch {
		case active > 1:
			return nil, fmt.Errorf("template %q has %d active versions", name, active)
		case active == 0 && len(candidates) > 1:
			return nil, fmt.Errorf("template %q has %d versions but none is active (set %s=true on one ConfigMap)", name, len(candidates), TemplateActiveLabel)
		}
		sources = append(sources, candidates[selected].source)
	}
	return sources, nil
}

func sleep(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
