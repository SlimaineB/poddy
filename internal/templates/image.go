package templates

import "strings"

type ImageRewriter struct {
	Registry string
	Map      map[string]string
}

func NewImageRewriter(registry string, mappings map[string]string) ImageRewriter {
	return ImageRewriter{
		Registry: strings.TrimSuffix(strings.TrimSpace(registry), "/"),
		Map:      normalizeImageRegistryMap(mappings),
	}
}

func (r ImageRewriter) Rewrite(image string) string {
	image = strings.TrimSpace(image)
	if image == "" {
		return image
	}
	if r.alreadyMirrored(image) {
		return image
	}
	host, rest, hasHost := splitImageHost(image)
	key := host
	if key == "" {
		key = "docker.io"
	}
	if target, ok := r.lookup(key); ok {
		return applyImageMirror(image, target, host, rest, hasHost)
	}
	if target, ok := r.Map["*"]; ok {
		return applyImageMirror(image, target, host, rest, hasHost)
	}
	return applyImageMirror(image, r.Registry, host, rest, hasHost)
}

func RewriteImageRegistry(image, registry string) string {
	return NewImageRewriter(registry, nil).Rewrite(image)
}

func RewriteImageRegistryWithMap(image, registry string, mappings map[string]string) string {
	return NewImageRewriter(registry, mappings).Rewrite(image)
}

func (r ImageRewriter) lookup(host string) (string, bool) {
	if target, ok := r.Map[host]; ok {
		return target, true
	}
	switch host {
	case "docker.io":
		target, ok := r.Map["index.docker.io"]
		return target, ok
	case "index.docker.io":
		target, ok := r.Map["docker.io"]
		return target, ok
	default:
		return "", false
	}
}

func (r ImageRewriter) alreadyMirrored(image string) bool {
	if imageTarget(image, r.Registry) {
		return true
	}
	for _, target := range r.Map {
		if imageTarget(image, target) {
			return true
		}
	}
	return false
}

func imageTarget(image, target string) bool {
	target = strings.TrimSuffix(strings.TrimSpace(target), "/")
	if target == "" {
		return false
	}
	return image == target || strings.HasPrefix(image, target+"/")
}

func applyImageMirror(image, target, host, rest string, hasHost bool) string {
	target = strings.TrimSuffix(strings.TrimSpace(target), "/")
	if target == "" {
		return image
	}
	if imageTarget(image, target) {
		return image
	}
	if hasHost {
		return target + "/" + rest
	}
	return target + "/" + image
}

func splitImageHost(image string) (string, string, bool) {
	first, rest, ok := strings.Cut(image, "/")
	if !ok || !isRegistryHost(first) {
		return "", image, false
	}
	return strings.ToLower(first), rest, true
}

func normalizeImageRegistryMap(mappings map[string]string) map[string]string {
	if len(mappings) == 0 {
		return nil
	}
	out := make(map[string]string, len(mappings))
	for key, value := range mappings {
		key = strings.ToLower(strings.TrimSpace(key))
		if key == "" {
			continue
		}
		out[key] = strings.TrimSuffix(strings.TrimSpace(value), "/")
	}
	return out
}

func isRegistryHost(value string) bool {
	return value == "localhost" || strings.Contains(value, ".") || strings.Contains(value, ":")
}
