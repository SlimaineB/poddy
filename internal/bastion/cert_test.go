package bastion

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"poddy/internal/k8s"
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

type stubRemote struct{}

func (stubRemote) DialWorkspace(context.Context, string, int) (net.Conn, error) {
	return nil, errors.New("unused")
}

func (stubRemote) DialRegionWorkspace(context.Context, string, int) (net.Conn, error) {
	return nil, errors.New("unused")
}

func TestRegionKeyAuthorization(t *testing.T) {
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}
	pub := signer.PublicKey()
	fingerprint := ssh.FingerprintSHA256(pub)
	keys, err := sshkeys.Build(nil, []sshkeys.OwnedKey{{
		Owner: "alice@example.com",
		Line:  string(ssh.MarshalAuthorizedKey(pub)),
	}}, false)
	if err != nil {
		t.Fatal(err)
	}
	b := &Bastion{
		keys:   keys,
		Remote: stubRemote{},
		RegionOwner: func(_ context.Context, workspace string) (string, error) {
			if workspace == "missing" {
				return "", errors.New("not found")
			}
			return "alice@example.com", nil
		},
	}
	ctx := context.Background()
	if !b.regionAuthorized(ctx, nil, fingerprint, "dev") {
		t.Fatal("workspace owner key was not authorized")
	}
	if b.regionAuthorized(ctx, nil, ssh.FingerprintSHA256(testCertKey(t)), "dev") {
		t.Fatal("unknown key was authorized")
	}
	if b.regionAuthorized(ctx, nil, fingerprint, "missing") {
		t.Fatal("key was authorized for an unresolvable workspace")
	}
}

func TestSharedCertificateAuthorization(t *testing.T) {
	ca, err := sshca.Generate()
	if err != nil {
		t.Fatal(err)
	}
	keys, err := sshkeys.Parse(nil, false)
	if err != nil {
		t.Fatal(err)
	}
	store := k8s.NewMemoryAccessStore()
	b := &Bastion{CA: ca, keys: keys, Access: store}

	cert, err := ca.Sign(testCertKey(t), sshca.WorkspacePrincipal, "bob@example.com", "dev", false, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	permissions, err := b.publicKeyCallback(nil, cert)
	if err != nil {
		t.Fatal(err)
	}
	if b.authorize(permissions, "", "alice@example.com", "dev") {
		t.Fatal("shared user was authorized before the share")
	}
	if err := store.Share(context.Background(), "dev", "bob@example.com", "alice@example.com"); err != nil {
		t.Fatal(err)
	}
	if !b.authorize(permissions, "", "alice@example.com", "dev") {
		t.Fatal("shared user was not authorized")
	}
	if err := store.Unshare(context.Background(), "dev", "bob@example.com"); err != nil {
		t.Fatal(err)
	}
	if b.authorize(permissions, "", "alice@example.com", "dev") {
		t.Fatal("shared user was authorized after the share was removed")
	}
}
