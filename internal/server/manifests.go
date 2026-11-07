package server

import (
	"net/http"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/yaml"
)

func (s *Server) workspaceManifests(w http.ResponseWriter, r *http.Request) {
	id, ok := s.currentIdentity(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	cfg, err := s.loadRBAC(r)
	if err != nil {
		http.Error(w, "rbac: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if !s.isAdmin(id, cfg) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if s.Regions != nil {
		s.proxyWorkspace(w, r)
		return
	}

	name := r.PathValue("name")
	manifests, err := s.Workspaces.Manifests(r.Context(), name)
	if err != nil {
		if apierrors.IsNotFound(err) {
			http.Error(w, "workspace not found", http.StatusNotFound)
			return
		}
		writeK8sError(w, err, http.StatusInternalServerError)
		return
	}

	podYAML, err := yaml.Marshal(manifests.Pod)
	if err != nil {
		http.Error(w, "render pod manifest: "+err.Error(), http.StatusInternalServerError)
		return
	}
	pvcYAML := []byte{}
	if manifests.PVC != nil {
		pvcYAML, err = yaml.Marshal(manifests.PVC)
		if err != nil {
			http.Error(w, "render pvc manifest: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}
	parameters := manifests.Parameters
	if parameters == nil {
		parameters = map[string]string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"name":            manifests.Name,
		"template":        manifests.Template,
		"templateVersion": manifests.TemplateVersion,
		"parameters":      parameters,
		"podSource":       manifests.PodSource,
		"pod":             string(podYAML),
		"pvc":             string(pvcYAML),
		"redactedEnv":     nonNil(manifests.RedactedEnv),
	})
}
