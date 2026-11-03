package server

import (
	"encoding/json"
	"log"
	"net/http"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

func noCache(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		next.ServeHTTP(w, r)
	})
}

func writeK8sError(w http.ResponseWriter, err error, fallback int) {
	switch {
	case apierrors.IsUnauthorized(err):
		http.Error(w, "kubernetes unauthorized: check kubeconfig or service account", http.StatusUnauthorized)
	case apierrors.IsForbidden(err):
		http.Error(w, "kubernetes forbidden: check RBAC", http.StatusForbidden)
	default:
		http.Error(w, err.Error(), fallback)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("write response: %v", err)
	}
}

func nonNil(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
