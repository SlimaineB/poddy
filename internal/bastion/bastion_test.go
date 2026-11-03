package bastion

import "testing"

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
