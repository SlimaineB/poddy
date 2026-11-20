package mesh

import (
	"context"
	"strings"
	"testing"
)

func TestSanitizeHostname(t *testing.T) {
	cases := map[string]string{
		"Demo":                  "demo",
		"my workspace":          "my-workspace",
		"alice/Prod_01":         "alice-prod-01",
		"---":                   "",
		"a..b":                  "a-b",
		strings.Repeat("x", 80): strings.Repeat("x", maxHostname),
	}
	for input, want := range cases {
		if got := SanitizeHostname(input); got != want {
			t.Fatalf("SanitizeHostname(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestHostnamesAreStableAndScoped(t *testing.T) {
	if got := WorkspaceHostname("Demo"); got != "ws-demo" {
		t.Fatalf("WorkspaceHostname = %q", got)
	}
	if got := RegionHostname("eu"); got != "reg-eu" {
		t.Fatalf("RegionHostname = %q", got)
	}
	long := WorkspaceHostname(strings.Repeat("a", 80))
	if len(long) > maxHostname {
		t.Fatalf("hostname too long: %d", len(long))
	}
	if !strings.HasPrefix(long, "ws-") {
		t.Fatalf("hostname lost prefix: %q", long)
	}
}

func TestStartRequiresAuthKey(t *testing.T) {
	if _, err := Start(context.Background(), Config{Hostname: "portal"}); err == nil {
		t.Fatal("expected an error without an auth key")
	}
}
