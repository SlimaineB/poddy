package bastion

import (
	"context"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"k8s.io/client-go/kubernetes/fake"

	"poddy/internal/k8s"
)

func TestInteractiveAuthorized(t *testing.T) {
	b := &Bastion{Access: k8s.NewAccessStore(fake.NewClientset(), "poddy", "")}
	admin := &ssh.Permissions{Extensions: map[string]string{"admin": "true"}}

	if !b.interactiveAuthorized(nil, "admin@example.com", "demo", "alice@example.com") {
		t.Fatal("non-admin certificate should not be gated")
	}
	if !b.interactiveAuthorized(admin, "alice@example.com", "demo", "alice@example.com") {
		t.Fatal("owner should always be allowed")
	}
	if !b.interactiveAuthorized(admin, "admin@example.com", "demo", "") {
		t.Fatal("legacy workspace without owner should remain open")
	}
	if b.interactiveAuthorized(admin, "admin@example.com", "demo", "alice@example.com") {
		t.Fatal("admin was allowed without a grant")
	}

	if err := b.Access.Approve(context.Background(), "demo", "admin@example.com", "alice@example.com", time.Minute); err != nil {
		t.Fatal(err)
	}
	if !b.interactiveAuthorized(admin, "admin@example.com", "demo", "alice@example.com") {
		t.Fatal("admin was denied despite an active grant")
	}

	if err := b.Access.Revoke(context.Background(), "demo", "admin@example.com"); err != nil {
		t.Fatal(err)
	}
	if b.interactiveAuthorized(admin, "admin@example.com", "demo", "alice@example.com") {
		t.Fatal("admin was allowed after the grant was revoked")
	}
}
