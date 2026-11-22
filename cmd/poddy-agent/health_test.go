package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAgentHealthReportsMeshReadiness(t *testing.T) {
	health := newAgentHealth()
	server := httptest.NewServer(health.handler())
	defer server.Close()

	resp, err := http.Get(server.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d", resp.StatusCode)
	}

	resp, err = http.Get(server.URL + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("initial readiness status = %d", resp.StatusCode)
	}

	health.setReady(true, "")
	resp, err = http.Get(server.URL + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ready status = %d", resp.StatusCode)
	}
}
