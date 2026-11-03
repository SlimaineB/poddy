package userstore

import (
	"context"
	"testing"

	"k8s.io/client-go/kubernetes/fake"
)

func TestUpsertPreservesFirstSeen(t *testing.T) {
	store := NewStore(fake.NewSimpleClientset(), "poddy", "")
	ctx := context.Background()
	first, err := store.Upsert(ctx, User{Subject: "sub-1", Email: "alice@example.com", Username: "alice", Groups: []string{"backend"}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Upsert(ctx, User{Subject: "sub-1", Email: "alice@example.com", Username: "alice", Groups: []string{"backend", "go-users"}})
	if err != nil {
		t.Fatal(err)
	}
	if !second.FirstSeen.Equal(first.FirstSeen) {
		t.Fatalf("first seen changed: %v != %v", second.FirstSeen, first.FirstSeen)
	}
	if len(second.Groups) != 2 {
		t.Fatalf("groups = %+v", second.Groups)
	}
	users, err := store.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 || users[0].Email != "alice@example.com" {
		t.Fatalf("users = %+v", users)
	}
}

func TestSearch(t *testing.T) {
	store := NewStore(fake.NewSimpleClientset(), "poddy", "")
	ctx := context.Background()
	for _, user := range []User{
		{Subject: "1", Email: "alice@example.com", Username: "alice", Name: "Alice"},
		{Subject: "2", Email: "bob@example.com", Username: "bob", Name: "Bob"},
	} {
		if _, err := store.Upsert(ctx, user); err != nil {
			t.Fatal(err)
		}
	}
	users, err := store.Search(ctx, "ali", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 || users[0].Username != "alice" {
		t.Fatalf("users = %+v", users)
	}
}
