package templates

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"strconv"
	"text/template"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/strategicpatch"
	"sigs.k8s.io/yaml"
)

const (
	workspaceContainer     = "workspace"
	browserVSCodeContainer = "code-server"
	workspaceVolume        = "workspace"
)

//go:embed files/defaults.pod.yaml
var defaultPodTemplate []byte

// ApplyDefaults merges a rendered template over poddy's default pod. It is a
// mutation pass, not a schema: fields in the template win, lists merge with the
// usual Kubernetes keys, and the template can replace lists with `$patch`.
func ApplyDefaults(raw []byte, data Data) (*corev1.Pod, error) {
	if data.Parameters == nil {
		data.Parameters = map[string]string{}
	}
	if data.WorkspacePath == "" {
		data.WorkspacePath = WorkspacePath
	}
	if data.SSHCommand == "" {
		data.SSHCommand = SSHCommand
	}
	if data.Storage == "" {
		data.Storage = data.Parameters["storage"]
	}
	if data.Storage != "" && data.PVCName == "" {
		return nil, fmt.Errorf("PVC name is required for persistent storage")
	}

	defaults, err := renderDefaultPod(data)
	if err != nil {
		return nil, err
	}
	patchJSON, err := yaml.YAMLToJSON(raw)
	if err != nil {
		return nil, fmt.Errorf("decode pod template: %w", err)
	}
	var patchPod corev1.Pod
	if err := json.Unmarshal(patchJSON, &patchPod); err != nil {
		return nil, fmt.Errorf("decode pod template: %w", err)
	}
	replaceVolumes(defaults, &patchPod)
	overrideContainerLists(defaults, &patchPod)

	defaultJSON, err := json.Marshal(defaults)
	if err != nil {
		return nil, fmt.Errorf("encode default pod: %w", err)
	}
	mergedJSON, err := strategicpatch.StrategicMergePatch(defaultJSON, patchJSON, corev1.Pod{})
	if err != nil {
		return nil, fmt.Errorf("merge pod defaults: %w", err)
	}
	var merged corev1.Pod
	if err := json.Unmarshal(mergedJSON, &merged); err != nil {
		return nil, fmt.Errorf("decode merged pod: %w", err)
	}
	container := containerByName(merged.Spec.Containers, workspaceContainer)
	if container == nil {
		return nil, fmt.Errorf("template must define a container named %q", workspaceContainer)
	}
	if container.Image == "" {
		return nil, fmt.Errorf("workspace container image is required")
	}
	return &merged, nil
}

func renderDefaultPod(data Data) (*corev1.Pod, error) {
	tmpl, err := template.New("defaults").Funcs(template.FuncMap{"quote": strconv.Quote}).Parse(string(defaultPodTemplate))
	if err != nil {
		return nil, fmt.Errorf("parse default pod: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("render default pod: %w", err)
	}
	var pod corev1.Pod
	if err := yaml.Unmarshal(buf.Bytes(), &pod); err != nil {
		return nil, fmt.Errorf("decode default pod: %w", err)
	}
	return &pod, nil
}

// replaceVolumes lets a template fully replace a default volume with the same
// name instead of merging volume sources (which can produce invalid volumes).
func replaceVolumes(defaults, pod *corev1.Pod) {
	if len(pod.Spec.Volumes) == 0 {
		return
	}
	replaced := map[string]bool{}
	for _, volume := range pod.Spec.Volumes {
		replaced[volume.Name] = true
	}
	kept := defaults.Spec.Volumes[:0]
	for _, volume := range defaults.Spec.Volumes {
		if !replaced[volume.Name] {
			kept = append(kept, volume)
		}
	}
	defaults.Spec.Volumes = kept
}

// overrideContainerLists removes default list entries when the template
// explicitly controls that list, so the template's value is not merged with an
// unwanted default (for example a custom SSH port or a different home mount).
func overrideContainerLists(defaults, pod *corev1.Pod) {
	for _, container := range pod.Spec.Containers {
		if container.Name != workspaceContainer {
			continue
		}
		target := containerByName(defaults.Spec.Containers, workspaceContainer)
		if target == nil {
			return
		}
		if len(container.Ports) > 0 {
			target.Ports = nil
		}
		for _, mount := range container.VolumeMounts {
			if mount.Name != workspaceVolume {
				continue
			}
			kept := target.VolumeMounts[:0]
			for _, item := range target.VolumeMounts {
				if item.Name != workspaceVolume {
					kept = append(kept, item)
				}
			}
			target.VolumeMounts = kept
			break
		}
		return
	}
}

func containerByName(containers []corev1.Container, name string) *corev1.Container {
	for i := range containers {
		if containers[i].Name == name {
			return &containers[i]
		}
	}
	return nil
}

func hasEnv(env []corev1.EnvVar, name string) bool {
	for _, item := range env {
		if item.Name == name {
			return true
		}
	}
	return false
}

func hasPort(ports []corev1.ContainerPort, port int32) bool {
	for _, item := range ports {
		if item.ContainerPort == port {
			return true
		}
	}
	return false
}

func hasVolumeMount(mounts []corev1.VolumeMount, name string) bool {
	for _, item := range mounts {
		if item.Name == name {
			return true
		}
	}
	return false
}
