package app

import (
	"crypto/ed25519"
	"crypto/rand"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestTrustedKeyReplacesComment(t *testing.T) {
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ssh.NewPublicKey(public)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key))) + " user@example.com"
	got := trustedKey(line)
	if !strings.HasSuffix(got, " admin") {
		t.Fatalf("trustedKey() = %q", got)
	}
	if strings.Contains(got, "user@example.com") {
		t.Fatalf("trustedKey() kept old comment: %q", got)
	}
	if _, _, _, _, err := ssh.ParseAuthorizedKey([]byte(got)); err != nil {
		t.Fatalf("trustedKey() produced invalid key: %v", err)
	}
}
