package k8s

import "strings"

const (
	AppVSCode     = "vscode"
	AppCursor     = "cursor"
	AppPyCharm    = "pycharm"
	AppJupyterLab = "jupyterlab"
	AppVSCodeWeb  = "vscode-web"
)

var supportedApps = map[string]struct{}{
	AppVSCode:     {},
	AppCursor:     {},
	AppPyCharm:    {},
	AppJupyterLab: {},
	AppVSCodeWeb:  {},
}

func NormalizeApps(apps []string) []string {
	if apps == nil {
		return nil
	}
	out := make([]string, 0, len(apps))
	seen := make(map[string]struct{}, len(apps))
	for _, app := range apps {
		app = strings.ToLower(strings.TrimSpace(app))
		if _, ok := supportedApps[app]; !ok {
			continue
		}
		if _, ok := seen[app]; ok {
			continue
		}
		seen[app] = struct{}{}
		out = append(out, app)
	}
	return out
}

func hasApp(apps []string, name string) bool {
	for _, app := range apps {
		if app == name {
			return true
		}
	}
	return false
}

func appsFromAnnotations(annotations map[string]string) []string {
	raw, ok := annotations[annotationApps]
	if !ok {
		return nil
	}
	if strings.TrimSpace(raw) == "" {
		return []string{}
	}
	return NormalizeApps(strings.Split(raw, ","))
}
