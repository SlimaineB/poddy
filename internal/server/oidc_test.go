package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"poddy/internal/auth"
)

func newOIDCTestServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()

	var provider *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                provider.URL,
			"authorization_endpoint":                provider.URL + "/auth",
			"token_endpoint":                        provider.URL + "/token",
			"jwks_uri":                              provider.URL + "/keys",
			"userinfo_endpoint":                     provider.URL + "/userinfo",
			"response_types_supported":              []string{"code"},
			"subject_types_supported":               []string{"public"},
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	provider = httptest.NewServer(mux)
	t.Cleanup(provider.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	o, err := auth.New(ctx, auth.Config{
		Issuer:      provider.URL,
		ClientID:    "poddy",
		RedirectURL: "http://portal.example/oidc/callback",
		SessionKey:  []byte("0123456789abcdef0123456789abcdef"),
	})
	if err != nil {
		t.Fatal(err)
	}

	s := newTestServer("")
	s.OIDC = o
	return s, provider
}

func TestOIDCLoginPageShowsSSO(t *testing.T) {
	s, _ := newOIDCTestServer(t)
	rec := request(t, s, http.MethodGet, "/login", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Continue with SSO") {
		t.Fatalf("login page does not offer SSO: %s", rec.Body.String())
	}
}

func TestOIDCLoginRedirectsToProvider(t *testing.T) {
	s, provider := newOIDCTestServer(t)
	rec := request(t, s, http.MethodGet, "/oidc/login?next=%2F", nil)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); !strings.HasPrefix(loc, provider.URL+"/auth?") {
		t.Fatalf("location = %q", loc)
	}

	found := false
	for _, c := range rec.Result().Cookies() {
		if c.Name == oidcCookie && c.Value != "" {
			found = true
		}
	}
	if !found {
		t.Fatalf("flow cookie not set: %+v", rec.Result().Cookies())
	}
}

func TestOIDCMiddlewareRedirectsToSSO(t *testing.T) {
	s, _ := newOIDCTestServer(t)
	rec := request(t, s, http.MethodGet, "/", nil)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/oidc/login?next=%2F" {
		t.Fatalf("location = %q", loc)
	}
}

func TestOIDCSessionAuthorizesAPI(t *testing.T) {
	s, _ := newOIDCTestServer(t)
	session, err := s.OIDC.SignSession(auth.User{
		Subject:  "sub-1",
		Username: "alice",
		Email:    "alice@example.com",
	})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: session})
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["username"] != "alice" || body["method"] != "oidc" {
		t.Fatalf("body = %+v", body)
	}
}
