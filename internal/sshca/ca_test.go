package sshca

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func testPublicKey(t *testing.T) ssh.PublicKey {
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

func TestSignAndVerify(t *testing.T) {
	ca, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	cert, err := ca.Sign(testPublicKey(t), WorkspacePrincipal, "alice@example.com", "dev", false, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if cert.KeyId != "alice@example.com" {
		t.Fatalf("key id = %q", cert.KeyId)
	}
	if len(cert.ValidPrincipals) != 1 || cert.ValidPrincipals[0] != WorkspacePrincipal {
		t.Fatalf("principals = %v", cert.ValidPrincipals)
	}
	if got := Workspace(cert); got != "dev" {
		t.Fatalf("workspace = %q", got)
	}
	if Admin(cert) {
		t.Fatal("unexpected admin extension")
	}
	if err := ca.VerifyCert(cert); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

func TestVerifyRejectsOtherCA(t *testing.T) {
	ca, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	other, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	cert, err := ca.Sign(testPublicKey(t), WorkspacePrincipal, "alice@example.com", "", false, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := other.VerifyCert(cert); err == nil {
		t.Fatal("certificate signed by another CA was accepted")
	}
}

func TestVerifyRejectsExpiredCertificate(t *testing.T) {
	ca, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	cert, err := ca.Sign(testPublicKey(t), WorkspacePrincipal, "alice@example.com", "", false, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	if err := ca.VerifyCert(cert); err == nil {
		t.Fatal("expired certificate was accepted")
	}
}

func TestAuthorizedKey(t *testing.T) {
	ca, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	line := ca.AuthorizedKey()
	for _, want := range []string{"cert-authority", `principals="dev"`, `from="127.0.0.1,::1"`, "ssh-ed25519"} {
		if !strings.Contains(line, want) {
			t.Fatalf("authorized key %q missing %q", line, want)
		}
	}
	public, _, options, _, err := ssh.ParseAuthorizedKey([]byte(line))
	if err != nil {
		t.Fatalf("parse authorized key: %v", err)
	}
	if public.Type() != ssh.KeyAlgoED25519 {
		t.Fatalf("key type = %q", public.Type())
	}
	if len(options) == 0 {
		t.Fatal("missing authorized_keys options")
	}
}

func TestAuthorizedKeyUnrestricted(t *testing.T) {
	ca, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	line := ca.AuthorizedKeyUnrestricted()
	if strings.Contains(line, "from=") {
		t.Fatalf("unrestricted authorized key contains from option: %q", line)
	}
	for _, want := range []string{"cert-authority", `principals="dev"`, "ssh-ed25519"} {
		if !strings.Contains(line, want) {
			t.Fatalf("authorized key %q missing %q", line, want)
		}
	}
}

func TestPrivateKeyPEMRoundTrip(t *testing.T) {
	ca, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := NewFromPEM(ca.PrivateKeyPEM())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := ssh.FingerprintSHA256(loaded.PublicKey()), ssh.FingerprintSHA256(ca.PublicKey()); got != want {
		t.Fatalf("fingerprint = %s, want %s", got, want)
	}
}

func TestNewFromPKCS8PEM(t *testing.T) {
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := NewFromPEM(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	if err != nil {
		t.Fatal(err)
	}
	if ca.PublicKey() == nil {
		t.Fatal("missing public key")
	}
}
