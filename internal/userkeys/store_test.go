package userkeys

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"poddy/internal/sshkeys"
)

func testPublicKey(t *testing.T) string {
	t.Helper()
	_, private, err := generateKey()
	if err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSpace(string(sshMarshalPublic(private.PublicKey())))
	return line + " alice@laptop"
}

func TestAddBindsOwnerAndRejectsDuplicates(t *testing.T) {
	client := fake.NewSimpleClientset()
	store := NewStore(client, "poddy", "")
	key := testPublicKey(t)

	saved, err := store.Add(context.Background(), "Alice@Example.com", key)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Owner != "alice@example.com" {
		t.Fatalf("owner = %q", saved.Owner)
	}
	if _, err := store.Add(context.Background(), "bob@example.com", key); err != ErrDuplicate {
		t.Fatalf("duplicate error = %v", err)
	}
	if _, err := store.Add(context.Background(), "alice@example.com", key); err != ErrDuplicate {
		t.Fatalf("same-owner duplicate error = %v", err)
	}
	if err := store.Remove(context.Background(), "bob@example.com", saved.ID); err != ErrNotFound {
		t.Fatalf("cross-owner remove error = %v", err)
	}
	if err := store.Remove(context.Background(), "alice@example.com", saved.ID); err != nil {
		t.Fatal(err)
	}
}

func TestOwnedKeyringUsesRegistryOwner(t *testing.T) {
	client := fake.NewSimpleClientset()
	store := NewStore(client, "poddy", "")
	key := testPublicKey(t)
	saved, err := store.Add(context.Background(), "alice@example.com", key)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := store.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	set, err := sshkeys.Build(nil, Owned(keys), false)
	if err != nil {
		t.Fatal(err)
	}
	if !set.Authorize(saved.Fingerprint, "alice@example.com") {
		t.Fatal("alice key should authorize alice")
	}
	if set.Authorize(saved.Fingerprint, "bob@example.com") {
		t.Fatal("alice key must not authorize bob")
	}
}

func TestStoreCreatesLabeledConfigMap(t *testing.T) {
	client := fake.NewSimpleClientset()
	store := NewStore(client, "poddy", "")
	if _, err := store.Add(context.Background(), "alice@example.com", testPublicKey(t)); err != nil {
		t.Fatal(err)
	}
	cm, err := client.CoreV1().ConfigMaps("poddy").Get(context.Background(), DefaultConfigMap, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if cm.Labels[Label] != "true" {
		t.Fatalf("labels = %+v", cm.Labels)
	}
	if !strings.Contains(cm.Data[dataKey], "alice@example.com") {
		t.Fatalf("data = %q", cm.Data[dataKey])
	}
}
