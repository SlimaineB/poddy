package config

import (
	"reflect"
	"testing"
)

func TestParseImageRegistryMap(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		want    map[string]string
		wantErr bool
	}{
		{
			name: "pairs",
			raw:  "ghcr.io=mirror.example.com/ghcr,docker.io=mirror.example.com/docker",
			want: map[string]string{
				"ghcr.io":   "mirror.example.com/ghcr",
				"docker.io": "mirror.example.com/docker",
			},
		},
		{
			name: "json",
			raw:  `{"ghcr.io":"mirror.example.com/ghcr","*":"mirror.example.com/cache"}`,
			want: map[string]string{
				"ghcr.io": "mirror.example.com/ghcr",
				"*":       "mirror.example.com/cache",
			},
		},
		{
			name: "empty",
			raw:  "",
		},
		{
			name: "empty json",
			raw:  "{}",
		},
		{
			name: "opt out",
			raw:  "ghcr.io=",
			want: map[string]string{"ghcr.io": ""},
		},
		{
			name:    "missing target",
			raw:     "ghcr.io",
			wantErr: true,
		},
		{
			name:    "empty host",
			raw:     "=mirror.example.com",
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseImageRegistryMap(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %v", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}
