package auth

import (
	"net/http/httptest"
	"testing"
	"time"
)

func testOIDC() *OIDC {
	return &OIDC{
		cfg: Config{
			Issuer:     "https://id.example.com",
			SessionTTL: time.Hour,
		},
		key: []byte("0123456789abcdef0123456789abcdef"),
	}
}

func TestSessionRoundTrip(t *testing.T) {
	o := testOIDC()
	token, err := o.SignSession(User{
		Subject:  "sub-1",
		Username: "alice",
		Email:    "alice@example.com",
		Groups:   []string{"devs"},
	})
	if err != nil {
		t.Fatal(err)
	}

	user, err := o.ParseSession(token)
	if err != nil {
		t.Fatal(err)
	}
	if user.Subject != "sub-1" || user.Username != "alice" || user.Email != "alice@example.com" {
		t.Fatalf("user = %+v", user)
	}
	if len(user.Groups) != 1 || user.Groups[0] != "devs" {
		t.Fatalf("groups = %+v", user.Groups)
	}
}

func TestSessionTampered(t *testing.T) {
	o := testOIDC()
	token, err := o.SignSession(User{Username: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := o.ParseSession(token + "x"); err == nil {
		t.Fatal("expected tampered session to be rejected")
	}
}

func TestSessionExpired(t *testing.T) {
	o := testOIDC()
	o.cfg.SessionTTL = -time.Minute
	token, err := o.SignSession(User{Username: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := o.ParseSession(token); err == nil {
		t.Fatal("expected expired session to be rejected")
	}
}

func TestSessionWrongKey(t *testing.T) {
	o := testOIDC()
	token, err := o.SignSession(User{Username: "alice"})
	if err != nil {
		t.Fatal(err)
	}

	other := testOIDC()
	other.key = []byte("another-key-another-key-another-ke")
	if _, err := other.ParseSession(token); err == nil {
		t.Fatal("expected wrong key to be rejected")
	}
}

func TestFlowRoundTrip(t *testing.T) {
	o := testOIDC()
	flow := &Flow{
		State:       "state",
		Nonce:       "nonce",
		Verifier:    "verifier",
		Next:        "/workspaces",
		RedirectURL: "https://poddy.example.com/oidc/callback",
		IssuedAt:    time.Now().Unix(),
	}
	value, err := o.SignFlow(flow)
	if err != nil {
		t.Fatal(err)
	}
	got, err := o.ParseFlow(value)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != flow.State || got.Nonce != flow.Nonce || got.Verifier != flow.Verifier || got.Next != flow.Next {
		t.Fatalf("flow = %+v", got)
	}
}

func TestRedirectURL(t *testing.T) {
	req := httptest.NewRequest("GET", "http://internal/", nil)
	req.Host = "poddy.example.com"
	req.Header.Set("X-Forwarded-Proto", "https")

	if got, want := RedirectURL(req, ""), "https://poddy.example.com/oidc/callback"; got != want {
		t.Fatalf("redirect = %q, want %q", got, want)
	}
	if got, want := RedirectURL(req, "https://custom.example/cb"), "https://custom.example/cb"; got != want {
		t.Fatalf("configured redirect = %q, want %q", got, want)
	}
}

func TestStringListClaim(t *testing.T) {
	claims := map[string]any{
		"groups": []any{"devs", "platform"},
		"csv":    "a, b,c",
	}
	if got := stringListClaim(claims, "groups"); len(got) != 2 || got[1] != "platform" {
		t.Fatalf("groups = %+v", got)
	}
	if got := stringListClaim(claims, "csv"); len(got) != 3 {
		t.Fatalf("csv = %+v", got)
	}
}

func TestUserDisplay(t *testing.T) {
	cases := []struct {
		user User
		want string
	}{
		{User{Username: "alice", Email: "a@example.com"}, "alice"},
		{User{Email: "a@example.com"}, "a@example.com"},
		{User{Subject: "sub"}, "sub"},
	}
	for _, tc := range cases {
		if got := tc.user.Display(); got != tc.want {
			t.Fatalf("Display(%+v) = %q, want %q", tc.user, got, tc.want)
		}
	}
}

func TestAuthorizeGroupsAndUsers(t *testing.T) {
	o := testOIDC()

	o.cfg.AllowedGroups = []string{"devs"}
	if err := o.authorize(User{Username: "alice", Groups: []string{"devs"}}); err != nil {
		t.Fatalf("allowed group rejected: %v", err)
	}
	if err := o.authorize(User{Username: "bob", Groups: []string{"ops"}}); err == nil {
		t.Fatal("disallowed group accepted")
	}

	o.cfg.AllowedGroups = nil
	o.cfg.AllowedUsers = []string{"alice@example.com"}
	if err := o.authorize(User{Email: "alice@example.com"}); err != nil {
		t.Fatalf("allowed user rejected: %v", err)
	}
	if err := o.authorize(User{Email: "bob@example.com"}); err == nil {
		t.Fatal("disallowed user accepted")
	}
}
