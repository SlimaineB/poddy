package templates

import "testing"

func TestRewriteImageRegistry(t *testing.T) {
	cases := []struct {
		name     string
		image    string
		registry string
		want     string
	}{
		{"ghcr", "ghcr.io/slimaineb/poddy-workspace-base:latest", "mirror.example.com", "mirror.example.com/slimaineb/poddy-workspace-base:latest"},
		{"docker hub namespace", "gitpod/openvscode-server:latest", "mirror.example.com", "mirror.example.com/gitpod/openvscode-server:latest"},
		{"docker hub official", "busybox:1.36", "mirror.example.com", "mirror.example.com/busybox:1.36"},
		{"same registry", "mirror.example.com/slimaineb/poddy:1", "mirror.example.com", "mirror.example.com/slimaineb/poddy:1"},
		{"registry path", "ghcr.io/team/poddy:1", "mirror.example.com/cache", "mirror.example.com/cache/team/poddy:1"},
		{"same registry path", "mirror.example.com/cache/team/poddy:1", "mirror.example.com/cache", "mirror.example.com/cache/team/poddy:1"},
		{"localhost registry", "localhost:5000/team/poddy:1", "mirror.example.com", "mirror.example.com/team/poddy:1"},
		{"trailing slash", "ghcr.io/team/poddy:1", "mirror.example.com/", "mirror.example.com/team/poddy:1"},
		{"empty registry", "ghcr.io/team/poddy:1", "", "ghcr.io/team/poddy:1"},
		{"empty image", "", "mirror.example.com", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := RewriteImageRegistry(tc.image, tc.registry); got != tc.want {
				t.Fatalf("RewriteImageRegistry(%q, %q) = %q, want %q", tc.image, tc.registry, got, tc.want)
			}
		})
	}
}
