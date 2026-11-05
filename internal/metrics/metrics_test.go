package metrics

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMetricsHandler(t *testing.T) {
	m := New()
	m.ObserveHTTP("GET", "GET /api/workspaces", 200, 10*time.Millisecond)
	m.WorkspaceOperation("create", "success")
	m.WorkspaceCreate("base", "success", 250*time.Millisecond)
	m.AuthAttempt("token", "success")
	m.TemplateReload("success")
	m.SetWorkspaceLister(func(context.Context) ([]WorkspaceInfo, error) {
		return []WorkspaceInfo{
			{Phase: "Running", Template: "base"},
			{Phase: "Running", Template: "base"},
			{Phase: "Stopped", Template: "go"},
		}, nil
	})

	req := httptest.NewRequest("GET", "/metrics", nil)
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`poddy_http_requests_total{method="GET",route="GET /api/workspaces",status="200"} 1`,
		`poddy_workspace_operations_total{operation="create",result="success"} 1`,
		`poddy_auth_attempts_total{method="token",result="success"} 1`,
		`poddy_template_reloads_total{result="success"} 1`,
		`poddy_workspaces{phase="Running",template="base"} 2`,
		`poddy_workspaces{phase="Stopped",template="go"} 1`,
		`poddy_build_info`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics body missing %q", want)
		}
	}
}

func TestNilMetricsAreSafe(t *testing.T) {
	var m *Metrics
	m.ObserveHTTP("GET", "GET /", 200, time.Second)
	m.WorkspaceOperation("create", "success")
	m.WorkspaceCreate("base", "success", time.Second)
	m.AuthAttempt("token", "success")
	m.TemplateReload("success")
	m.HTTPInFlight(1)
}
