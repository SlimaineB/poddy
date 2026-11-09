package bastion

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"poddy/internal/sshca"
	"poddy/internal/sshkeys"
)

func testCertKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}
	return signer.PublicKey()
}

func TestCertificateAuthenticationAndAuthorization(t *testing.T) {
	ca, err := sshca.Generate()
	if err != nil {
		t.Fatal(err)
	}
	keys, err := sshkeys.Parse(nil, false)
	if err != nil {
		t.Fatal(err)
	}
	b := &Bastion{CA: ca, keys: keys}

	cert, err := ca.Sign(testCertKey(t), sshca.WorkspacePrincipal, "alice@example.com", "dev", false, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	permissions, err := b.publicKeyCallback(nil, cert)
	if err != nil {
		t.Fatal(err)
	}
	if !b.authorize(permissions, "", "alice@example.com", "dev") {
		t.Fatal("owner was not authorized")
	}
	if b.authorize(permissions, "", "bob@example.com", "dev") {
		t.Fatal("different owner was authorized")
	}
	if b.authorize(permissions, "", "alice@example.com", "other") {
		t.Fatal("workspace outside certificate scope was authorized")
	}

	admin, err := ca.Sign(testCertKey(t), sshca.WorkspacePrincipal, "root@example.com", "", true, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	adminPermissions, err := b.publicKeyCallback(nil, admin)
	if err != nil {
		t.Fatal(err)
	}
	if !b.authorize(adminPermissions, "", "bob@example.com", "anything") {
		t.Fatal("admin certificate was not authorized")
	}

	other, err := sshca.Generate()
	if err != nil {
		t.Fatal(err)
	}
	b.CA = other
	if _, err := b.publicKeyCallback(nil, cert); err == nil {
		t.Fatal("certificate from another CA was accepted")
	}
}
