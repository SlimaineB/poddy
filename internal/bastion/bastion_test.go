package bastion

import "testing"

func TestConnectionLimiter(t *testing.T) {
	b := &Bastion{MaxConnections: 2, MaxConnectionsPerIP: 1, perIP: map[string]int{}}
	if !b.acquire("127.0.0.1") {
		t.Fatal("first connection rejected")
	}
	if b.acquire("127.0.0.1") {
		t.Fatal("second connection from the same IP accepted")
	}
	if !b.acquire("127.0.0.2") {
		t.Fatal("connection from another IP rejected")
	}
	if b.acquire("127.0.0.3") {
		t.Fatal("global connection limit not enforced")
	}
	b.release("127.0.0.1")
	if !b.acquire("127.0.0.1") {
		t.Fatal("connection rejected after release")
	}
}

func TestWorkspaceName(t *testing.T) {
	tests := map[string]string{
		"poddy-ssh-demo":       "demo",
		"poddy-demo":           "demo",
		"[poddy-ssh-demo]":     "demo",
		"[poddy-ssh-demo]:22":  "demo",
		"poddy-ssh-demo:22":    "demo",
		"  [poddy-ssh-demo]  ": "demo",
	}
	for host, want := range tests {
		if got := workspaceName(host); got != want {
			t.Errorf("workspaceName(%q) = %q, want %q", host, got, want)
		}
	}
}
