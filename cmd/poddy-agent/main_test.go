package main

import (
	"testing"
	"time"
)

func TestMeshUnhealthyTimeout(t *testing.T) {
	t.Setenv("PODDY_MESH_UNHEALTHY_TIMEOUT", "")
	if got := meshUnhealthyTimeout(); got != time.Minute {
		t.Fatalf("default timeout = %s, want 1m", got)
	}

	t.Setenv("PODDY_MESH_UNHEALTHY_TIMEOUT", "30s")
	if got := meshUnhealthyTimeout(); got != 30*time.Second {
		t.Fatalf("configured timeout = %s, want 30s", got)
	}

	t.Setenv("PODDY_MESH_UNHEALTHY_TIMEOUT", "invalid")
	if got := meshUnhealthyTimeout(); got != time.Minute {
		t.Fatalf("invalid timeout = %s, want 1m", got)
	}
}
