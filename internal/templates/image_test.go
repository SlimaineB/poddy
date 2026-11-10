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

func TestRewriteImageRegistryWithMap(t *testing.T) {
	cases := []struct {
		name     string
		image    string
		registry string
		mappings map[string]string
		want     string
	}{
		{
			name:     "exact host",
			image:    "ghcr.io/team/app:1",
			mappings: map[string]string{"ghcr.io": "mirror.example.com/ghcr"},
			want:     "mirror.example.com/ghcr/team/app:1",
		},
		{
			name:     "docker hub namespace",
			image:    "gitpod/openvscode-server:latest",
			mappings: map[string]string{"docker.io": "mirror.example.com/docker"},
			want:     "mirror.example.com/docker/gitpod/openvscode-server:latest",
		},
		{
			name:     "docker hub official",
			image:    "busybox:1.36",
			mappings: map[string]string{"docker.io": "mirror.example.com/docker"},
			want:     "mirror.example.com/docker/busybox:1.36",
		},
		{
			name:     "docker hub alias",
			image:    "index.docker.io/library/busybox:1.36",
			mappings: map[string]string{"docker.io": "mirror.example.com/docker"},
			want:     "mirror.example.com/docker/library/busybox:1.36",
		},
		{
			name:     "wildcard",
			image:    "quay.io/team/app:1",
			mappings: map[string]string{"*": "mirror.example.com/cache"},
			want:     "mirror.example.com/cache/team/app:1",
		},
		{
			name:     "exact wins over wildcard",
			image:    "ghcr.io/team/app:1",
			mappings: map[string]string{"ghcr.io": "mirror.example.com/ghcr", "*": "mirror.example.com/cache"},
			want:     "mirror.example.com/ghcr/team/app:1",
		},
		{
			name:     "fallback",
			image:    "quay.io/team/app:1",
			registry: "mirror.example.com",
			mappings: map[string]string{"ghcr.io": "mirror.example.com/ghcr"},
			want:     "mirror.example.com/team/app:1",
		},
		{
			name:     "empty target opts out",
			image:    "ghcr.io/team/app:1",
			registry: "mirror.example.com",
			mappings: map[string]string{"ghcr.io": ""},
			want:     "ghcr.io/team/app:1",
		},
		{
			name:     "already mirrored",
			image:    "mirror.example.com/ghcr/team/app:1",
			registry: "mirror.example.com",
			mappings: map[string]string{"ghcr.io": "mirror.example.com/ghcr"},
			want:     "mirror.example.com/ghcr/team/app:1",
		},
		{
			name:     "uppercase host key",
			image:    "ghcr.io/team/app:1",
			mappings: map[string]string{"GHCR.IO": "mirror.example.com/ghcr/"},
			want:     "mirror.example.com/ghcr/team/app:1",
		},
		{
			name:     "localhost registry",
			image:    "localhost:5000/team/app:1",
			mappings: map[string]string{"localhost:5000": "mirror.example.com/local"},
			want:     "mirror.example.com/local/team/app:1",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := RewriteImageRegistryWithMap(tc.image, tc.registry, tc.mappings); got != tc.want {
				t.Fatalf("RewriteImageRegistryWithMap(%q, %q, %v) = %q, want %q", tc.image, tc.registry, tc.mappings, got, tc.want)
			}
		})
	}
}
