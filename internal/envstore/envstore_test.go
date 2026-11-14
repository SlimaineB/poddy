package envstore

import (
	"reflect"
	"testing"

	"poddy/internal/secure"
)

func TestCanonicalTarget(t *testing.T) {
	cases := []struct {
		scope  Scope
		target string
		want   string
	}{
		{ScopeUser, " Alice@Example.com ", "alice@example.com"},
		{ScopeTeam, " backend ", "backend"},
		{ScopeTemplate, " base ", "base"},
		{ScopeWorkspace, " app ", "app"},
	}
	for _, tc := range cases {
		got, err := CanonicalTarget(tc.scope, tc.target)
		if err != nil {
			t.Fatal(err)
		}
		if got != tc.want {
			t.Fatalf("CanonicalTarget(%s, %q) = %q, want %q", tc.scope, tc.target, got, tc.want)
		}
	}
	if _, err := CanonicalTarget(ScopeUser, " "); err == nil {
		t.Fatal("empty target should fail")
	}
}

func TestValidateNameRejectsReserved(t *testing.T) {
	for _, name := range []string{"PODDY_PORTAL_URL", "KUBERNETES_SERVICE_HOST", "GIT_ASKPASS", "1BAD", "BAD-NAME"} {
		if err := ValidateName(name); err == nil {
			t.Fatalf("ValidateName(%q) should fail", name)
		}
	}
	for _, name := range []string{"API_TOKEN", "_PRIVATE", "A1"} {
		if err := ValidateName(name); err != nil {
			t.Fatalf("ValidateName(%q): %v", name, err)
		}
	}
}

func TestMergePrecedenceAndSecrets(t *testing.T) {
	key := secure.Key("test-key")
	secret, err := secure.Encrypt(key, []byte("secret-workspace"))
	if err != nil {
		t.Fatal(err)
	}
	store := &PostgresStore{key: key}
	rows := []row{
		{scope: ScopeTemplate, target: "base", name: "A", value: "template"},
		{scope: ScopeTemplate, target: "base", name: "T", value: "template"},
		{scope: ScopeTeam, target: "alpha", name: "A", value: "alpha"},
		{scope: ScopeTeam, target: "zeta", name: "A", value: "zeta"},
		{scope: ScopeTeam, target: "zeta", name: "Z", value: "zeta"},
		{scope: ScopeUser, target: "alice@example.com", name: "A", value: "user"},
		{scope: ScopeUser, target: "alice@example.com", name: "U", value: "user"},
		{scope: ScopeWorkspace, target: "app", name: "A", value: "workspace"},
		{scope: ScopeWorkspace, target: "app", name: "S", ciphertext: secret, secret: true},
	}
	resolved, err := store.merge(rows, "base", []string{"alpha", "zeta"}, "alice@example.com", "app")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"A": "workspace",
		"T": "template",
		"Z": "zeta",
		"U": "user",
		"S": "secret-workspace",
	}
	if !reflect.DeepEqual(resolved.Values, want) {
		t.Fatalf("values = %#v, want %#v", resolved.Values, want)
	}
	if !resolved.Secrets["S"] || resolved.Secrets["A"] {
		t.Fatalf("secrets = %#v", resolved.Secrets)
	}
}

func TestMergeSecretRequiresKey(t *testing.T) {
	store := &PostgresStore{}
	_, err := store.merge([]row{{scope: ScopeUser, target: "alice", name: "TOKEN", ciphertext: []byte("x"), secret: true}}, "", nil, "alice", "")
	if err == nil {
		t.Fatal("secret without key should fail")
	}
}

func TestSetRejectsOversizedValue(t *testing.T) {
	store := &PostgresStore{}
	err := store.Set(nil, ScopeUser, "alice", "TOKEN", string(make([]byte, 64*1024+1)), false)
	if err == nil {
		t.Fatal("oversized value should fail")
	}
}
