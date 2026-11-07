package sshkeys

import (
	"crypto/ed25519"
	"crypto/rand"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func testKey(t *testing.T, comment string) string {
	t.Helper()
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ssh.NewPublicKey(public)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
	return line + " " + comment
}

func fingerprint(t *testing.T, line string) string {
	t.Helper()
	public, _, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
	if err != nil {
		t.Fatal(err)
	}
	return ssh.FingerprintSHA256(public)
}

func TestAuthorizeByComment(t *testing.T) {
	alice := testKey(t, "alice@example.com")
	admin := testKey(t, "admin")
	unmapped := testKey(t, "unknown")
	set, err := Parse([]string{alice, admin, unmapped}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !set.Authorize(fingerprint(t, alice), "alice@example.com") {
		t.Fatal("alice should access her workspace")
	}
	if set.Authorize(fingerprint(t, alice), "bob@example.com") {
		t.Fatal("alice must not access bob's workspace")
	}
	if !set.Authorize(fingerprint(t, admin), "bob@example.com") {
		t.Fatal("admin should access every workspace")
	}
	if set.Authorize(fingerprint(t, unmapped), "alice@example.com") {
		t.Fatal("unmapped key must be denied by default")
	}
}

func TestForOwner(t *testing.T) {
	alice := testKey(t, "user:alice@example.com")
	admin := testKey(t, "*")
	unmapped := testKey(t, "unknown")
	set, err := Parse([]string{alice, admin, unmapped}, false)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(set.ForOwner("alice@example.com"), "\n")
	if !strings.Contains(got, alice) || !strings.Contains(got, admin) {
		t.Fatalf("ForOwner() = %q", got)
	}
	if strings.Contains(got, unmapped) {
		t.Fatalf("ForOwner() included unmapped key: %q", got)
	}
	if len(set.ForOwner("bob@example.com")) != 1 {
		t.Fatalf("bob should only get the admin key")
	}
}

func TestAllowUnmapped(t *testing.T) {
	unmapped := testKey(t, "unknown")
	set, err := Parse([]string{unmapped}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !set.Authorize(fingerprint(t, unmapped), "alice@example.com") {
		t.Fatal("legacy mode should allow unmapped keys")
	}
}

func TestOwnedKeyIgnoresComment(t *testing.T) {
	key := testKey(t, "admin")
	set, err := Build(nil, []OwnedKey{{Owner: "alice@example.com", Line: key}}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !set.Authorize(fingerprint(t, key), "alice@example.com") {
		t.Fatal("owner-bound key should authorize its real owner")
	}
	if set.Authorize(fingerprint(t, key), "bob@example.com") {
		t.Fatal("owner-bound key must ignore a forged admin comment")
	}
	if len(set.ForOwner("bob@example.com")) != 0 {
		t.Fatal("bob should not receive alice's key")
	}
}

func TestBuildRejectsDuplicateIdentity(t *testing.T) {
	alice := testKey(t, "user:alice@example.com")
	_, err := Build(nil, []OwnedKey{
		{Owner: "alice@example.com", Line: alice},
		{Owner: "bob@example.com", Line: alice},
	}, false)
	if err == nil {
		t.Fatal("duplicate key assigned to two owners should fail")
	}
}

func TestParsePublicKeyRejectsShortRSAAndOptions(t *testing.T) {
	alice := testKey(t, "alice")
	if _, _, _, _, err := ParsePublicKey(alice); err != nil {
		t.Fatalf("valid key rejected: %v", err)
	}
	withOptions := `command="echo pwned" ` + alice
	if _, _, _, _, err := ParsePublicKey(withOptions); err == nil {
		t.Fatal("authorized_keys options should be rejected")
	}
}
