package mesh

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tailscale.com/ipn/ipnstate"
	"tailscale.com/types/key"
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

func TestSelectPeerPrefersHealthyDuplicate(t *testing.T) {
	now := time.Now()
	peer := func(host string, online bool, addr string, seen time.Time) *ipnstate.PeerStatus {
		return &ipnstate.PeerStatus{HostName: host, Online: online, CurAddr: addr, LastSeen: seen}
	}

	online := selectPeer(&ipnstate.Status{Peer: map[key.NodePublic]*ipnstate.PeerStatus{
		key.NewNode().Public(): peer("ws-demo", false, "", now.Add(-time.Hour)),
		key.NewNode().Public(): peer("ws-demo", true, "", now.Add(-time.Minute)),
	}}, "ws-demo")
	if online == nil || !online.Online {
		t.Fatalf("online duplicate not selected: %+v", online)
	}

	direct := selectPeer(&ipnstate.Status{Peer: map[key.NodePublic]*ipnstate.PeerStatus{
		key.NewNode().Public(): peer("ws-demo", true, "", now),
		key.NewNode().Public(): peer("ws-demo", true, "192.0.2.10:41641", now.Add(-time.Hour)),
	}}, "ws-demo")
	if direct == nil || direct.CurAddr == "" {
		t.Fatalf("direct-path duplicate not selected: %+v", direct)
	}

	recent := selectPeer(&ipnstate.Status{Peer: map[key.NodePublic]*ipnstate.PeerStatus{
		key.NewNode().Public(): peer("ws-demo", true, "", now.Add(-time.Hour)),
		key.NewNode().Public(): peer("ws-demo", true, "", now),
	}}, "ws-demo")
	if recent == nil || !recent.LastSeen.Equal(now) {
		t.Fatalf("most recently seen duplicate not selected: %+v", recent)
	}

	exact := selectPeer(&ipnstate.Status{Peer: map[key.NodePublic]*ipnstate.PeerStatus{
		key.NewNode().Public(): peer("ws-demo-extra", true, "", now),
		key.NewNode().Public(): peer("ws-demo", false, "", now.Add(-time.Hour)),
	}}, "ws-demo")
	if exact == nil || exact.HostName != "ws-demo" {
		t.Fatalf("exact hostname should win over a different workspace: %+v", exact)
	}
}

func TestForceLoginForNewState(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TSNET_FORCE_LOGIN", "")
	forceLoginForNewState(dir)
	if got := os.Getenv("TSNET_FORCE_LOGIN"); got != "1" {
		t.Fatalf("fresh state did not force login: %q", got)
	}

	t.Setenv("TSNET_FORCE_LOGIN", "")
	if err := os.WriteFile(filepath.Join(dir, "tailscaled.state"), []byte("state"), 0o600); err != nil {
		t.Fatal(err)
	}
	forceLoginForNewState(dir)
	if got := os.Getenv("TSNET_FORCE_LOGIN"); got != "" {
		t.Fatalf("existing state should not force login: %q", got)
	}
}
