package server

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"poddy/internal/auth"
	"poddy/internal/rbac"
	"poddy/internal/sshkeys"
	"poddy/internal/userkeys"
)

func testSSHPublicKey(t *testing.T, comment string) string {
	t.Helper()
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ssh.NewPublicKey(public)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key))) + " " + comment
}

func enableSelfServiceSSH(t *testing.T, s *Server) {
	t.Helper()
	store := userkeys.NewStore(s.Workspaces.Client, "poddy", "")
	keyring, err := sshkeys.NewDynamic(nil, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	s.UserKeys = store
	s.SSHKeys = keyring
	s.Workspaces.SSHKeys = keyring
}

func TestSelfServiceSSHKeysAreOwnerScoped(t *testing.T) {
	s := newRBACServer(t, rbacTestConfig())
	enableSelfServiceSSH(t, s)
	alice := sessionFor(t, s, auth.User{Username: "alice", Email: "alice@example.com"})
	bob := sessionFor(t, s, auth.User{Username: "bob", Email: "bob@example.com"})
	key := testSSHPublicKey(t, "admin")

	rec := requestAs(t, s, alice, http.MethodPost, "/api/me/ssh-keys", map[string]string{"key": key})
	if rec.Code != http.StatusCreated {
		t.Fatalf("alice add = %d, body = %s", rec.Code, rec.Body.String())
	}
	var added sshKeyInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &added); err != nil {
		t.Fatal(err)
	}

	rec = requestAs(t, s, alice, http.MethodGet, "/api/me/ssh-keys", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), added.ID) {
		t.Fatalf("alice list = %d, body = %s", rec.Code, rec.Body.String())
	}
	rec = requestAs(t, s, bob, http.MethodGet, "/api/me/ssh-keys", nil)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), added.ID) {
		t.Fatalf("bob list leaked key = %d, body = %s", rec.Code, rec.Body.String())
	}
	if rec := requestAs(t, s, bob, http.MethodPost, "/api/me/ssh-keys", map[string]string{"key": key}); rec.Code != http.StatusConflict {
		t.Fatalf("bob duplicate add = %d", rec.Code)
	}
	if rec := requestAs(t, s, bob, http.MethodDelete, "/api/me/ssh-keys/"+added.ID, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("bob delete alice key = %d", rec.Code)
	}
	if rec := requestAs(t, s, alice, http.MethodDelete, "/api/me/ssh-keys/"+added.ID, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("alice delete own key = %d", rec.Code)
	}
}

func TestSelfServiceSSHKeyRejectsOptions(t *testing.T) {
	s := newRBACServer(t, rbacTestConfig())
	enableSelfServiceSSH(t, s)
	alice := sessionFor(t, s, auth.User{Username: "alice", Email: "alice@example.com"})
	key := testSSHPublicKey(t, "alice")
	rec := requestAs(t, s, alice, http.MethodPost, "/api/me/ssh-keys", map[string]string{"key": `command="id" ` + key})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("options key = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestSharedTokenIsNotAdminByDefault(t *testing.T) {
	s := newTestServer("secret")
	rec := request(t, s, http.MethodGet, "/api/me?token=secret", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["isAdmin"] != false {
		t.Fatalf("shared token must not be admin by default: %+v", body)
	}
}

func TestSharedTokenAdminRequiresExplicitOptIn(t *testing.T) {
	s := newTestServer("secret")
	s.TokenAdmin = true
	if !s.isAdmin(&identity{Method: "token", User: auth.User{Username: "token"}}, rbac.Config{}) {
		t.Fatal("tokenAdmin=true should grant admin")
	}
	s.TokenAdmin = false
	if s.isAdmin(&identity{Method: "token", User: auth.User{Username: "token"}}, rbac.Config{Admins: []string{"token"}}) {
		t.Fatal("RBAC must not silently promote the shared token")
	}
}

func TestSelfServiceSSHKeyPropagatesToWorkspaceSecret(t *testing.T) {
	s := newRBACServer(t, rbacTestConfig())
	enableSelfServiceSSH(t, s)
	alice := sessionFor(t, s, auth.User{Username: "alice", Email: "alice@example.com"})
	rec := requestAs(t, s, alice, http.MethodPost, "/api/workspaces", map[string]any{"name": "alice-ws", "template": "base"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d, body = %s", rec.Code, rec.Body.String())
	}
	key := testSSHPublicKey(t, "alice")
	rec = requestAs(t, s, alice, http.MethodPost, "/api/me/ssh-keys", map[string]string{"key": key})
	if rec.Code != http.StatusCreated {
		t.Fatalf("add = %d, body = %s", rec.Code, rec.Body.String())
	}
	secret, err := s.Workspaces.Client.CoreV1().Secrets("poddy").Get(context.Background(), "poddy-alice-ws-ssh-keys", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(secret.Data["authorized_keys"]), keyOnly(t, key)) {
		t.Fatalf("secret does not contain key: %q", secret.Data["authorized_keys"])
	}
}

func keyOnly(t *testing.T, line string) string {
	t.Helper()
	public, _, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(public)))
}
