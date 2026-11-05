package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"poddy/internal/metrics"
)

func TestMetricsMiddlewareUsesRoutePattern(t *testing.T) {
	m := metrics.New()
	s := &Server{Metrics: m}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /probe", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	handler := s.metricsMiddleware(s.audit(mux))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/probe", nil))

	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	body := rec.Body.String()
	want := `poddy_http_requests_total{method="GET",route="GET /probe",status="204"} 1`
	if !strings.Contains(body, want) {
		t.Fatalf("metrics body missing %q", want)
	}
}
