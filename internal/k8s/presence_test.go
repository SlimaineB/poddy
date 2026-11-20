package k8s

import (
	"strings"
	"testing"
)

func TestPresenceLeaseName(t *testing.T) {
	if got := presenceLeaseName("workspace", "Demo_App"); got != "poddy-tun-workspace-demo-app" {
		t.Fatalf("lease name = %q", got)
	}
	long := presenceLeaseName("workspace", strings.Repeat("a", 80))
	if len(long) > 63 {
		t.Fatalf("lease name too long: %d", len(long))
	}
}
