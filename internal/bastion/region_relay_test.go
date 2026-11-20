package bastion

import "testing"

func TestRegionRelayEndpoint(t *testing.T) {
	cases := []struct {
		raw       string
		workspace string
		want      string
		ok        bool
	}{
		{"http://poddy.poddy-system.svc:80", "demo", "ws://poddy.poddy-system.svc:80/api/internal/region/ssh?workspace=demo", true},
		{"https://portal.example.com/", "a b", "wss://portal.example.com/api/internal/region/ssh?workspace=a+b", true},
		{"ws://portal:8080/base/", "demo", "ws://portal:8080/base/api/internal/region/ssh?workspace=demo", true},
		{"ftp://portal", "demo", "", false},
		{"http://", "demo", "", false},
	}
	for _, tc := range cases {
		got, err := regionRelayEndpoint(tc.raw, tc.workspace)
		if tc.ok {
			if err != nil || got != tc.want {
				t.Errorf("regionRelayEndpoint(%q) = %q, %v", tc.raw, got, err)
			}
			continue
		}
		if err == nil {
			t.Errorf("regionRelayEndpoint(%q) unexpectedly succeeded: %q", tc.raw, got)
		}
	}
}
