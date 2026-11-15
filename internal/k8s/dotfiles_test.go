package k8s

import (
	"context"
	"testing"

	"poddy/internal/dotfiles"
)

func TestCreateAppliesDotfiles(t *testing.T) {
	m := newTestManager()
	m.Dotfiles = func(_ context.Context, owner string) (dotfiles.Config, error) {
		if owner != "alice@example.com" {
			t.Fatalf("owner = %q", owner)
		}
		return dotfiles.Config{URL: "https://github.com/alice/dotfiles.git", Branch: "work"}, nil
	}
	ctx := context.Background()

	if _, err := m.Create(ctx, CreateRequest{Name: "app", Owner: "alice@example.com"}); err != nil {
		t.Fatal(err)
	}
	pod, err := m.Get(ctx, "app")
	if err != nil {
		t.Fatal(err)
	}
	if got := envValue(pod, ContainerName, envDotfilesURL); got != "https://github.com/alice/dotfiles.git" {
		t.Fatalf("dotfiles URL = %q", got)
	}
	if got := envValue(pod, ContainerName, envDotfilesBranch); got != "work" {
		t.Fatalf("dotfiles branch = %q", got)
	}
}

func TestStartRefreshesDotfiles(t *testing.T) {
	m := newTestManager()
	ctx := context.Background()
	cfg := dotfiles.Config{URL: "https://github.com/alice/dotfiles.git", Branch: "main"}
	m.Dotfiles = func(context.Context, string) (dotfiles.Config, error) {
		return cfg, nil
	}

	if _, err := m.Create(ctx, CreateRequest{Name: "app", Owner: "alice@example.com"}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Stop(ctx, "app"); err != nil {
		t.Fatal(err)
	}
	cfg = dotfiles.Config{}
	if _, err := m.Start(ctx, "app"); err != nil {
		t.Fatal(err)
	}
	pod, err := m.Get(ctx, "app")
	if err != nil {
		t.Fatal(err)
	}
	if got := envValue(pod, ContainerName, envDotfilesURL); got != "" {
		t.Fatalf("dotfiles URL = %q, want empty", got)
	}
	if got := envValue(pod, ContainerName, envDotfilesBranch); got != "" {
		t.Fatalf("dotfiles branch = %q, want empty", got)
	}
}
