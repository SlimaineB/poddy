package templates

import "strings"

func RewriteImageRegistry(image, registry string) string {
	image = strings.TrimSpace(image)
	registry = strings.TrimSuffix(strings.TrimSpace(registry), "/")
	if image == "" || registry == "" {
		return image
	}

	first, rest, ok := strings.Cut(image, "/")
	if ok && isRegistryHost(first) {
		if first == registry {
			return image
		}
		return registry + "/" + rest
	}
	return registry + "/" + image
}

func isRegistryHost(value string) bool {
	return value == "localhost" || strings.Contains(value, ".") || strings.Contains(value, ":")
}
