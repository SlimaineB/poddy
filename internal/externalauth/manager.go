package externalauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"poddy/internal/secure"

	"poddy/internal/templates"
)

var (
	// ErrReauth is returned when a token is missing, expired, or cannot be refreshed.
	ErrReauth = errors.New("external authentication required")
)

// AuthRequiredError tells the portal which providers a workspace still needs.
type AuthRequiredError struct {
	Missing []string
}

func (e *AuthRequiredError) Error() string {
	if len(e.Missing) == 0 {
		return ErrReauth.Error()
	}
	return fmt.Sprintf("connect external auth first: %s", strings.Join(e.Missing, ", "))
}

// Flow is the short-lived OAuth2 state stored in a signed cookie.
type Flow struct {
	State       string `json:"state"`
	Verifier    string `json:"verifier,omitempty"`
	Provider    string `json:"provider"`
	User        string `json:"user"`
	Next        string `json:"next"`
	RedirectURL string `json:"redirectURL"`
	IssuedAt    int64  `json:"issuedAt"`
}

// Status describes a provider connection for the current user.
type Status struct {
	ID              string     `json:"id"`
	DisplayName     string     `json:"displayName"`
	DisplayIcon     string     `json:"displayIcon,omitempty"`
	Type            string     `json:"type"`
	Authenticated   bool       `json:"authenticated"`
	ExpiresAt       *time.Time `json:"expiresAt,omitempty"`
	HasRefreshToken bool       `json:"hasRefreshToken"`
	LoginURL        string     `json:"loginUrl"`
}

// Manager runs external OAuth2 flows, refreshes tokens, and prepares workspace
// credentials.
type Manager struct {
	providers map[string]*Provider
	order     []string
	store     *Store
	signKey   []byte
	http      *http.Client
}

// NewManager builds a manager from configured providers.
func NewManager(providers []Provider, store *Store, signingSecret string) *Manager {
	m := &Manager{
		providers: map[string]*Provider{},
		store:     store,
		http:      http.DefaultClient,
	}
	for i := range providers {
		provider := providers[i]
		m.providers[provider.ID] = &provider
		m.order = append(m.order, provider.ID)
	}
	if signingSecret == "" {
		buf := make([]byte, 32)
		if _, err := rand.Read(buf); err == nil {
			m.signKey = buf
		}
	} else {
		sum := sha256.Sum256([]byte(signingSecret))
		m.signKey = sum[:]
	}
	return m
}

// Enabled reports whether any provider is configured.
func (m *Manager) Enabled() bool { return m != nil && len(m.providers) > 0 }

// Providers returns providers in configuration order.
func (m *Manager) Providers() []Provider {
	if m == nil {
		return nil
	}
	out := make([]Provider, 0, len(m.order))
	for _, id := range m.order {
		out = append(out, *m.providers[id])
	}
	return out
}

// Provider returns one configured provider.
func (m *Manager) Provider(id string) (*Provider, bool) {
	if m == nil {
		return nil, false
	}
	provider, ok := m.providers[id]
	return provider, ok
}

// Begin starts an OAuth2 authorization flow and returns the provider URL.
func (m *Manager) Begin(providerID, user, next, redirectURL string) (string, *Flow, error) {
	provider, ok := m.Provider(providerID)
	if !ok {
		return "", nil, fmt.Errorf("unknown external auth provider %q", providerID)
	}
	state, err := secure.RandomString(32)
	if err != nil {
		return "", nil, err
	}
	verifier := ""
	opts := []oauth2.AuthCodeOption{}
	if provider.PKCE {
		verifier = oauth2.GenerateVerifier()
		opts = append(opts, oauth2.S256ChallengeOption(verifier))
	}
	flow := &Flow{
		State:       state,
		Verifier:    verifier,
		Provider:    providerID,
		User:        user,
		Next:        next,
		RedirectURL: redirectURL,
		IssuedAt:    time.Now().Unix(),
	}
	cfg := m.oauthConfig(provider, redirectURL)
	return cfg.AuthCodeURL(state, opts...), flow, nil
}

// SignFlow encodes OAuth state so it can travel in a short-lived cookie.
func (m *Manager) SignFlow(flow *Flow) (string, error) {
	payload, err := json.Marshal(flow)
	if err != nil {
		return "", err
	}
	return secure.Sign(m.signKey, payload), nil
}

// ParseFlow decodes and verifies a signed OAuth state cookie.
func (m *Manager) ParseFlow(value string) (*Flow, error) {
	payload, err := secure.Verify(m.signKey, value)
	if err != nil {
		return nil, err
	}
	var flow Flow
	if err := json.Unmarshal(payload, &flow); err != nil {
		return nil, err
	}
	return &flow, nil
}

// Disconnect removes a user's stored token for a provider.
func (m *Manager) Disconnect(ctx context.Context, user, providerID string) error {
	if m == nil {
		return nil
	}
	return m.store.Delete(ctx, user, providerID)
}

// Complete exchanges an authorization code, stores the token, and returns it.
func (m *Manager) Complete(ctx context.Context, providerID, user, code string, flow *Flow) (Token, error) {
	if flow == nil {
		return Token{}, errors.New("missing external auth state")
	}
	if flow.Provider != providerID || flow.User != user {
		return Token{}, errors.New("external auth state does not match the signed-in user")
	}
	if time.Since(time.Unix(flow.IssuedAt, 0)) > 10*time.Minute {
		return Token{}, errors.New("external auth expired, please try again")
	}
	provider, ok := m.Provider(providerID)
	if !ok {
		return Token{}, fmt.Errorf("unknown external auth provider %q", providerID)
	}

	opts := []oauth2.AuthCodeOption{}
	if flow.Verifier != "" {
		opts = append(opts, oauth2.VerifierOption(flow.Verifier))
	}
	cfg := m.oauthConfig(provider, flow.RedirectURL)
	token, err := cfg.Exchange(ctx, code, opts...)
	if err != nil {
		return Token{}, fmt.Errorf("external auth exchange: %w", err)
	}
	stored := tokenFromOAuth(token)
	if err := m.store.Put(ctx, user, providerID, stored); err != nil {
		return Token{}, err
	}
	return stored, nil
}

// Token returns a valid access token, refreshing it when needed.
func (m *Manager) Token(ctx context.Context, user, providerID string) (Token, error) {
	provider, ok := m.Provider(providerID)
	if !ok {
		return Token{}, fmt.Errorf("unknown external auth provider %q", providerID)
	}
	stored, found, err := m.store.Get(ctx, user, providerID)
	if err != nil {
		return Token{}, err
	}
	if !found || stored.AccessToken == "" {
		return Token{}, ErrReauth
	}
	if !stored.Expired(time.Now()) {
		return stored, nil
	}
	if stored.RefreshToken == "" {
		return Token{}, fmt.Errorf("%w: no refresh token", ErrReauth)
	}

	cfg := m.oauthConfig(provider, "")
	source := cfg.TokenSource(ctx, &oauth2.Token{
		AccessToken:  stored.AccessToken,
		RefreshToken: stored.RefreshToken,
		TokenType:    stored.TokenType,
		Expiry:       stored.Expiry,
	})
	refreshed, err := source.Token()
	if err != nil {
		return Token{}, fmt.Errorf("%w: refresh failed: %v", ErrReauth, err)
	}
	next := tokenFromOAuth(refreshed)
	if next.RefreshToken == "" {
		next.RefreshToken = stored.RefreshToken
	}
	if err := m.store.Put(ctx, user, providerID, next); err != nil {
		return Token{}, err
	}
	return next, nil
}

// Status returns connection status for the requested provider IDs. An empty
// list returns every configured provider.
func (m *Manager) Status(ctx context.Context, user string, ids []string) ([]Status, error) {
	if m == nil {
		return nil, nil
	}
	selected := ids
	if len(selected) == 0 {
		selected = m.order
	}
	out := make([]Status, 0, len(selected))
	for _, id := range selected {
		provider, ok := m.Provider(id)
		if !ok {
			out = append(out, Status{ID: id, DisplayName: id, LoginURL: "/external-auth/" + url.PathEscape(id) + "/login"})
			continue
		}
		status := Status{
			ID:          provider.ID,
			DisplayName: provider.DisplayName,
			DisplayIcon: provider.DisplayIcon,
			Type:        provider.Type,
			LoginURL:    "/external-auth/" + url.PathEscape(provider.ID) + "/login",
		}
		token, found, err := m.store.Get(ctx, user, id)
		if err != nil {
			return nil, err
		}
		if found && token.AccessToken != "" {
			status.Authenticated = true
			status.HasRefreshToken = token.RefreshToken != ""
			if !token.Expiry.IsZero() {
				expiry := token.Expiry
				status.ExpiresAt = &expiry
			}
		}
		out = append(out, status)
	}
	return out, nil
}

// Prepare returns environment variables for a workspace. It refreshes tokens
// and fails with AuthRequiredError when a required provider is not connected.
func (m *Manager) Prepare(ctx context.Context, user string, requirements []templates.ExternalAuth) (map[string]string, error) {
	if m == nil || len(requirements) == 0 {
		return nil, nil
	}
	env := map[string]string{}
	missing := []string{}
	for _, requirement := range requirements {
		provider, ok := m.Provider(requirement.ID)
		if !ok {
			if requirement.Optional {
				continue
			}
			return nil, fmt.Errorf("external auth provider %q is not configured", requirement.ID)
		}
		token, err := m.Token(ctx, user, requirement.ID)
		if err != nil {
			if requirement.Optional && errors.Is(err, ErrReauth) {
				continue
			}
			if errors.Is(err, ErrReauth) {
				missing = append(missing, requirement.ID)
				continue
			}
			return nil, err
		}
		names := requirement.Env
		if len(names) == 0 {
			names = provider.Env
		}
		for _, name := range names {
			env[name] = token.AccessToken
		}
		env[providerEnvName(provider.ID)] = token.AccessToken
	}
	if len(missing) > 0 {
		return nil, &AuthRequiredError{Missing: missing}
	}
	return env, nil
}

// GitCredentials returns the username/password pair for a Git HTTPS prompt.
func (m *Manager) GitCredentials(ctx context.Context, user string, requirements []templates.ExternalAuth, prompt string) (string, string, error) {
	if m == nil {
		return "", "", errors.New("external auth is not configured")
	}
	matches := make([]*Provider, 0, 1)
	for _, requirement := range requirements {
		provider, ok := m.Provider(requirement.ID)
		if !ok {
			continue
		}
		if provider.MatchHost(prompt) {
			matches = append(matches, provider)
		}
	}
	if len(matches) == 0 {
		return "", "", fmt.Errorf("no external auth provider matches this Git host")
	}
	if len(matches) > 1 {
		ids := make([]string, 0, len(matches))
		for _, provider := range matches {
			ids = append(ids, provider.ID)
		}
		return "", "", fmt.Errorf("multiple external auth providers match this Git host: %s", strings.Join(ids, ", "))
	}
	provider := matches[0]
	token, err := m.Token(ctx, user, provider.ID)
	if err != nil {
		return "", "", err
	}
	return provider.Username, token.AccessToken, nil
}

// SignWorkspace returns a bearer token that a workspace can use to ask the
// portal for refreshed external auth tokens.
func (m *Manager) SignWorkspace(workspace, owner string) (string, error) {
	claims := workspaceClaims{
		Workspace: workspace,
		Owner:     owner,
		Expires:   time.Now().Add(30 * 24 * time.Hour).Unix(),
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	return secure.Sign(m.signKey, payload), nil
}

// VerifyWorkspace validates a workspace bearer token and returns its owner.
func (m *Manager) VerifyWorkspace(value, workspace string) (string, error) {
	payload, err := secure.Verify(m.signKey, value)
	if err != nil {
		return "", err
	}
	var claims workspaceClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", err
	}
	if claims.Workspace != workspace {
		return "", errors.New("workspace token does not match this workspace")
	}
	if claims.Expires < time.Now().Unix() {
		return "", errors.New("workspace token expired")
	}
	return claims.Owner, nil
}

type workspaceClaims struct {
	Workspace string `json:"workspace"`
	Owner     string `json:"owner"`
	Expires   int64  `json:"expires"`
}

func (m *Manager) oauthConfig(provider *Provider, redirectURL string) oauth2.Config {
	return oauth2.Config{
		ClientID:     provider.ClientID,
		ClientSecret: provider.ClientSecret,
		Endpoint: oauth2.Endpoint{
			AuthURL:  provider.AuthURL,
			TokenURL: provider.TokenURL,
		},
		RedirectURL: redirectURL,
		Scopes:      provider.Scopes,
	}
}

func tokenFromOAuth(token *oauth2.Token) Token {
	if token == nil {
		return Token{}
	}
	stored := Token{
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		TokenType:    token.TokenType,
		Expiry:       token.Expiry,
		UpdatedAt:    time.Now().UTC(),
	}
	if raw, ok := token.Extra("scope").(string); ok && raw != "" {
		stored.Scopes = splitList(raw)
	}
	return stored
}

func providerEnvName(id string) string {
	var b strings.Builder
	b.WriteString("PODDY_EXTERNAL_AUTH_")
	for _, r := range strings.ToUpper(id) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	b.WriteString("_TOKEN")
	return b.String()
}
