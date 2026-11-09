package server

import (
	"encoding/json"
	"net/http"
	"testing"

	"poddy/internal/k8s"
)

func TestWorkspaceScheduleAPI(t *testing.T) {
	s := newTestServer("")
	schedule := map[string]any{
		"enabled":  true,
		"timezone": "Europe/Paris",
		"start":    "09:00",
		"stop":     "18:00",
	}
	rec := request(t, s, http.MethodPost, "/api/workspaces", map[string]any{
		"name":     "app",
		"schedule": schedule,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var ws k8s.Workspace
	if err := json.Unmarshal(rec.Body.Bytes(), &ws); err != nil {
		t.Fatal(err)
	}
	if ws.Schedule == nil || ws.Schedule.Timezone != "Europe/Paris" {
		t.Fatalf("created schedule = %+v", ws.Schedule)
	}

	rec = request(t, s, http.MethodGet, "/api/workspaces/app", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("details status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var details map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &details); err != nil {
		t.Fatal(err)
	}
	got, ok := details["schedule"].(map[string]any)
	if !ok || got["start"] != "09:00" {
		t.Fatalf("details schedule = %+v", details["schedule"])
	}

	rec = request(t, s, http.MethodPut, "/api/workspaces/app/schedule", map[string]any{
		"enabled":  true,
		"timezone": "UTC",
		"start":    "22:00",
		"stop":     "06:00",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("update status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &ws); err != nil {
		t.Fatal(err)
	}
	if ws.Schedule == nil || ws.Schedule.Timezone != "UTC" || ws.Schedule.Start != "22:00" {
		t.Fatalf("updated schedule = %+v", ws.Schedule)
	}

	rec = request(t, s, http.MethodPut, "/api/workspaces/app/schedule", map[string]any{
		"enabled":  true,
		"timezone": "Mars/Olympus",
		"start":    "09:00",
		"stop":     "18:00",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid timezone status = %d, body = %s", rec.Code, rec.Body.String())
	}
}
