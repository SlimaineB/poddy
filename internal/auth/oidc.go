package auth

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"poddy/internal/secure"
)

// User describes an authenticated identity coming from the OIDC provider.
type User struct {
	Subject  string   `json:"sub"`
	Username string   `json:"username"`
	Email    string   `json:"email"`
	Name     string   `json:"name"`
	Groups   []string `json:"groups,omitempty"`
}

// Display returns the most human friendly label for the user.
func (u User) Display() string {
	switch {
	case u.Username != "":
		return u.Username
	case u.Email != "":
		return u.Email
	case u.Name != "":
		return u.Name
	default:
		return u.Subject
	}
}

// Config holds the OIDC provider settings.
type Config struct {
	Issuer        string
	ClientID      string
	ClientSecret  string
	RedirectURL   string
	Scopes        []string
	AllowedGroups []string
	AllowedUsers  []string
	UsernameClaim string
	EmailClaim    string
	GroupsClaim   string
	SessionKey    []byte
	SessionTTL    time.Duration
}

// OIDC implements the OpenID Connect authorization code flow with PKCE.
type OIDC struct {
	cfg      Config
	provider *oidc.Provider
	verifier *oidc.IDTokenVerifier
	oauth    oauth2.Config
	key      []byte
}

type flowState struct {
	State       string `json:"state"`
	Nonce       string `json:"nonce"`
	Verifier    string `json:"verifier"`
	Next        string `json:"next"`
	RedirectURL string `json:"redirect_url"`
	IssuedAt    int64  `json:"issued_at"`
}

type session struct {
	User    User   `json:"user"`
	Expires int64  `json:"expires"`
	Issuer  string `json:"issuer"`
}

// New builds an OIDC client and performs provider discovery.
func New(ctx context.Context, cfg Config) (*OIDC, error) {
	if cfg.Issuer == "" {
		return nil, errors.New("oidc issuer is required")
	}
	if cfg.ClientID == "" {
		return nil, errors.New("oidc client id is required")
	}
	if cfg.UsernameClaim == "" {
		cfg.UsernameClaim = "preferred_username"
	}
	if cfg.EmailClaim == "" {
		cfg.EmailClaim = "email"
	}
	if cfg.GroupsClaim == "" {
		cfg.GroupsClaim = "groups"
	}
	if len(cfg.Scopes) == 0 {
		cfg.Scopes = []string{oidc.ScopeOpenID, "profile", "email", "groups"}
	}
	if cfg.SessionTTL <= 0 {
		cfg.SessionTTL = 12 * time.Hour
	}

	key := cfg.SessionKey
	if len(key) == 0 {
		key = make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, fmt.Errorf("generate session key: %w", err)
		}
	}

	provider, err := oidc.NewProvider(ctx, cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery %s: %w", cfg.Issuer, err)
	}

	o := &OIDC{
		cfg:      cfg,
		provider: provider,
		verifier: provider.Verifier(&oidc.Config{ClientID: cfg.ClientID}),
		key:      key,
		oauth: oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			Endpoint:     provider.Endpoint(),
			Scopes:       cfg.Scopes,
			RedirectURL:  cfg.RedirectURL,
		},
	}
	return o, nil
}

// Enabled reports whether OIDC authentication is configured.
func (o *OIDC) Enabled() bool { return o != nil }

// SessionTTL returns the configured session lifetime.
func (o *OIDC) SessionTTL() time.Duration {
	if o == nil {
		return 0
	}
	return o.cfg.SessionTTL
}

// Issuer returns the configured issuer URL.
func (o *OIDC) Issuer() string {
	if o == nil {
		return ""
	}
	return o.cfg.Issuer
}

// BeginURL returns the provider authorization URL for a login attempt.
// The returned flow state must be stored by the caller and passed back to Callback.
func (o *OIDC) BeginURL(next, redirectURL string) (string, *Flow, error) {
	state, err := secure.RandomString(32)
	if err != nil {
		return "", nil, err
	}
	nonce, err := secure.RandomString(32)
	if err != nil {
		return "", nil, err
	}
	verifier := oauth2.GenerateVerifier()

	flow := &Flow{
		State:       state,
		Nonce:       nonce,
		Verifier:    verifier,
		Next:        next,
		RedirectURL: redirectURL,
		IssuedAt:    time.Now().Unix(),
	}

	opts := []oauth2.AuthCodeOption{
		oauth2.SetAuthURLParam("nonce", nonce),
		oauth2.S256ChallengeOption(verifier),
	}
	if redirectURL != "" {
		oauthCopy := o.oauth
		oauthCopy.RedirectURL = redirectURL
		return oauthCopy.AuthCodeURL(state, opts...), flow, nil
	}
	return o.oauth.AuthCodeURL(state, opts...), flow, nil
}

// Flow holds the transient state of a login attempt.
type Flow struct {
	State       string `json:"state"`
	Nonce       string `json:"nonce"`
	Verifier    string `json:"verifier"`
	Next        string `json:"next"`
	RedirectURL string `json:"redirect_url"`
	IssuedAt    int64  `json:"issued_at"`
}

// Complete exchanges the authorization code and verifies the ID token.
func (o *OIDC) Complete(ctx context.Context, code string, flow *Flow) (User, error) {
	if flow == nil {
		return User{}, errors.New("missing login state")
	}
	if time.Since(time.Unix(flow.IssuedAt, 0)) > 10*time.Minute {
		return User{}, errors.New("login expired, please try again")
	}

	oauthCopy := o.oauth
	if flow.RedirectURL != "" {
		oauthCopy.RedirectURL = flow.RedirectURL
	}

	token, err := oauthCopy.Exchange(ctx, code, oauth2.VerifierOption(flow.Verifier))
	if err != nil {
		return User{}, fmt.Errorf("oidc exchange: %w", err)
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		return User{}, errors.New("oidc response has no id_token")
	}

	idToken, err := o.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return User{}, fmt.Errorf("oidc verify: %w", err)
	}
	if idToken.Nonce != flow.Nonce {
		return User{}, errors.New("oidc nonce mismatch")
	}

	var claims map[string]any
	if err := idToken.Claims(&claims); err != nil {
		return User{}, fmt.Errorf("oidc claims: %w", err)
	}

	// Some providers expose groups/profile data only through the userinfo
	// endpoint. Merge it in without overriding ID token claims.
	if info, err := o.provider.UserInfo(ctx, oauth2.StaticTokenSource(token)); err == nil {
		var extra map[string]any
		if err := info.Claims(&extra); err == nil {
			for key, value := range extra {
				if _, exists := claims[key]; !exists {
					claims[key] = value
				}
			}
		}
	}

	user := User{
		Subject:  idToken.Subject,
		Username: stringClaim(claims, o.cfg.UsernameClaim),
		Email:    stringClaim(claims, o.cfg.EmailClaim),
		Name:     stringClaim(claims, "name"),
		Groups:   stringListClaim(claims, o.cfg.GroupsClaim),
	}
	if user.Username == "" {
		user.Username = user.Email
	}
	if user.Name == "" {
		user.Name = user.Username
	}

	if err := o.authorize(user); err != nil {
		return User{}, err
	}
	return user, nil
}

func (o *OIDC) authorize(user User) error {
	if len(o.cfg.AllowedGroups) > 0 {
		allowed := false
		for _, group := range o.cfg.AllowedGroups {
			for _, have := range user.Groups {
				if strings.EqualFold(group, have) {
					allowed = true
					break
				}
			}
			if allowed {
				break
			}
		}
		if !allowed {
			return fmt.Errorf("user %q is not a member of an allowed group", user.Display())
		}
	}

	if len(o.cfg.AllowedUsers) > 0 {
		allowed := false
		for _, user2 := range o.cfg.AllowedUsers {
			if strings.EqualFold(user2, user.Username) ||
				strings.EqualFold(user2, user.Email) ||
				strings.EqualFold(user2, user.Subject) {
				allowed = true
				break
			}
		}
		if !allowed {
			return fmt.Errorf("user %q is not allowed", user.Display())
		}
	}
	return nil
}

// SignSession returns a signed, self-contained session token.
func (o *OIDC) SignSession(user User) (string, error) {
	payload, err := json.Marshal(session{
		User:    user,
		Expires: time.Now().Add(o.cfg.SessionTTL).Unix(),
		Issuer:  o.cfg.Issuer,
	})
	if err != nil {
		return "", err
	}
	return secure.Sign(o.key, payload), nil
}

// ParseSession validates a session token and returns the user.
func (o *OIDC) ParseSession(value string) (User, error) {
	payload, err := secure.Verify(o.key, value)
	if err != nil {
		return User{}, err
	}
	var s session
	if err := json.Unmarshal(payload, &s); err != nil {
		return User{}, err
	}
	if s.Expires < time.Now().Unix() {
		return User{}, errors.New("session expired")
	}
	if s.Issuer != "" && o.cfg.Issuer != "" && s.Issuer != o.cfg.Issuer {
		return User{}, errors.New("session issuer mismatch")
	}
	return s.User, nil
}

// SignFlow encodes a login flow so it can travel in a short-lived cookie.
func (o *OIDC) SignFlow(flow *Flow) (string, error) {
	payload, err := json.Marshal(flow)
	if err != nil {
		return "", err
	}
	return secure.Sign(o.key, payload), nil
}

// ParseFlow decodes a login flow from its cookie value.
func (o *OIDC) ParseFlow(value string) (*Flow, error) {
	payload, err := secure.Verify(o.key, value)
	if err != nil {
		return nil, err
	}
	var flow Flow
	if err := json.Unmarshal(payload, &flow); err != nil {
		return nil, err
	}
	return &flow, nil
}

func stringClaim(claims map[string]any, name string) string {
	if name == "" {
		return ""
	}
	value, ok := claims[name]
	if !ok {
		return ""
	}
	switch v := value.(type) {
	case string:
		return v
	case []any:
		if len(v) > 0 {
			if s, ok := v[0].(string); ok {
				return s
			}
		}
	case float64:
		return fmt.Sprintf("%v", v)
	}
	return ""
}

func stringListClaim(claims map[string]any, name string) []string {
	if name == "" {
		return nil
	}
	value, ok := claims[name]
	if !ok {
		return nil
	}
	switch v := value.(type) {
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case string:
		fields := strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' })
		return fields
	}
	return nil
}

// RedirectURL resolves the callback URL for the current request.
// It prefers the configured URL and falls back to the request, honouring
// X-Forwarded-Proto and X-Forwarded-Host set by ingress controllers.
func RedirectURL(r *http.Request, configured string) string {
	if configured != "" {
		return configured
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		scheme = strings.TrimSpace(strings.Split(proto, ",")[0])
	}
	host := r.Host
	if forwarded := r.Header.Get("X-Forwarded-Host"); forwarded != "" {
		host = strings.TrimSpace(strings.Split(forwarded, ",")[0])
	}
	if host == "" {
		return ""
	}
	u := url.URL{Scheme: scheme, Host: host, Path: "/oidc/callback"}
	return u.String()
}
