package dotfiles

import (
	"context"
	"testing"
)

func TestNormalize(t *testing.T) {
	cases := []struct {
		name    string
		input   Config
		want    Config
		wantErr bool
	}{
		{name: "https defaults branch", input: Config{URL: "https://github.com/alice/dotfiles.git"}, want: Config{URL: "https://github.com/alice/dotfiles.git", Branch: "main"}},
		{name: "ssh scp syntax", input: Config{URL: "git@github.com:alice/dotfiles.git", Branch: "work"}, want: Config{URL: "git@github.com:alice/dotfiles.git", Branch: "work"}},
		{name: "ssh url", input: Config{URL: "ssh://git@github.com/alice/dotfiles.git"}, want: Config{URL: "ssh://git@github.com/alice/dotfiles.git", Branch: "main"}},
		{name: "empty clears branch", input: Config{URL: " ", Branch: "work"}, want: Config{}},
		{name: "unsupported scheme", input: Config{URL: "http://example.com/dotfiles.git"}, wantErr: true},
		{name: "credentials", input: Config{URL: "https://token@example.com/dotfiles.git"}, wantErr: true},
		{name: "password", input: Config{URL: "ssh://git:secret@example.com/dotfiles.git"}, wantErr: true},
		{name: "query", input: Config{URL: "https://example.com/dotfiles.git?token=secret"}, wantErr: true},
		{name: "bad branch", input: Config{URL: "https://example.com/dotfiles.git", Branch: "-main"}, wantErr: true},
		{name: "traversal branch", input: Config{URL: "https://example.com/dotfiles.git", Branch: "work/../main"}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Normalize(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("Normalize(%+v) succeeded", tc.input)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("Normalize(%+v) = %+v, want %+v", tc.input, got, tc.want)
			}
		})
	}
}

func TestEffective(t *testing.T) {
	ctx := context.Background()
	store := NewMemory()
	global := Config{URL: "https://example.com/global.git", Branch: "main"}

	cfg, source, err := Effective(ctx, store, "Alice@Example.com", global)
	if err != nil {
		t.Fatal(err)
	}
	if source != SourceGlobal || cfg != global {
		t.Fatalf("global fallback = %+v, %q", cfg, source)
	}

	if err := store.Set(ctx, "alice@example.com", Config{URL: "https://example.com/alice.git"}); err != nil {
		t.Fatal(err)
	}
	cfg, source, err = Effective(ctx, store, "ALICE@example.com", global)
	if err != nil {
		t.Fatal(err)
	}
	if source != SourceUser || cfg.URL != "https://example.com/alice.git" || cfg.Branch != "main" {
		t.Fatalf("user config = %+v, %q", cfg, source)
	}

	if err := store.Delete(ctx, "alice@example.com"); err != nil {
		t.Fatal(err)
	}
	cfg, source, err = Effective(ctx, store, "alice@example.com", Config{})
	if err != nil {
		t.Fatal(err)
	}
	if source != "" || !cfg.Empty() {
		t.Fatalf("empty config = %+v, %q", cfg, source)
	}
}
