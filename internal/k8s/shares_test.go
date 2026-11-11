package k8s

import (
	"context"
	"testing"
	"time"

	"k8s.io/client-go/kubernetes/fake"
)

func TestShareStore(t *testing.T) {
	store := NewShareStore(fake.NewClientset(), "poddy", "")
	ctx := context.Background()
	now := time.Now().UTC()
	share := Share{
		Token:     "token-1",
		Workspace: "demo",
		Port:      3000,
		Path:      "/api/workspaces/demo/ports/3000/",
		Access:    ShareAccessInternal,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.Set(ctx, share); err != nil {
		t.Fatal(err)
	}
	got, ok, err := store.GetByToken(ctx, "token-1")
	if err != nil || !ok {
		t.Fatalf("get token: ok=%v err=%v", ok, err)
	}
	if got.Access != ShareAccessInternal || got.Path != share.Path {
		t.Fatalf("share = %+v", got)
	}
	got, ok, err = store.GetByTarget(ctx, "demo", share.Path)
	if err != nil || !ok || got.Token != "token-1" {
		t.Fatalf("get target: ok=%v err=%v share=%+v", ok, err, got)
	}

	share.Token = "token-2"
	share.Access = ShareAccessPublic
	if err := store.Set(ctx, share); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.GetByToken(ctx, "token-1"); err != nil || ok {
		t.Fatalf("old token still present: ok=%v err=%v", ok, err)
	}
	got, ok, err = store.GetByToken(ctx, "token-2")
	if err != nil || !ok || got.Access != ShareAccessPublic {
		t.Fatalf("new token: ok=%v err=%v share=%+v", ok, err, got)
	}

	other := Share{
		Token:     "token-3",
		Workspace: "other",
		Path:      "/api/workspaces/other/ports/3000/",
		Access:    ShareAccessPublic,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.Set(ctx, other); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteWorkspace(ctx, "demo"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.GetByToken(ctx, "token-2"); err != nil || ok {
		t.Fatalf("demo share still present: ok=%v err=%v", ok, err)
	}
	if _, ok, err := store.GetByToken(ctx, "token-3"); err != nil || !ok {
		t.Fatalf("other share missing: ok=%v err=%v", ok, err)
	}
	if err := store.DeleteTarget(ctx, "other", other.Path); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.GetByToken(ctx, "token-3"); err != nil || ok {
		t.Fatalf("other share still present: ok=%v err=%v", ok, err)
	}
}

func TestNewShareToken(t *testing.T) {
	first, err := NewShareToken()
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewShareToken()
	if err != nil {
		t.Fatal(err)
	}
	if first == "" || first == second {
		t.Fatalf("tokens = %q %q", first, second)
	}
}
