package tunnel

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"poddy/internal/secure"
)

const (
	agentTokenKind  = "workspace-agent"
	defaultTokenTTL = time.Hour
)

type AgentClaims struct {
	Workspace string `json:"workspace"`
	Owner     string `json:"owner"`
	Kind      string `json:"kind"`
	Expires   int64  `json:"expires"`
}

type TokenSigner struct {
	key []byte
}

func NewTokenSigner(secret string) *TokenSigner {
	secret = strings.TrimSpace(secret)
	var key []byte
	if secret == "" {
		buf := make([]byte, 32)
		if _, err := rand.Read(buf); err == nil {
			key = buf
		}
	} else {
		key = secure.Key(secret)
	}
	return &TokenSigner{key: key}
}

func (t *TokenSigner) SignAgent(workspace, owner string, ttl time.Duration) (string, time.Time, error) {
	workspace = strings.TrimSpace(workspace)
	owner = strings.TrimSpace(owner)
	if workspace == "" || owner == "" {
		return "", time.Time{}, fmt.Errorf("workspace and owner are required")
	}
	if ttl <= 0 {
		ttl = defaultTokenTTL
	}
	expires := time.Now().Add(ttl).UTC()
	payload, err := json.Marshal(AgentClaims{
		Workspace: workspace,
		Owner:     owner,
		Kind:      agentTokenKind,
		Expires:   expires.Unix(),
	})
	if err != nil {
		return "", time.Time{}, err
	}
	return secure.Sign(t.workspaceKey(workspace), payload), expires, nil
}

func (t *TokenSigner) VerifyAgent(value string) (AgentClaims, error) {
	var claims AgentClaims
	value = strings.TrimSpace(value)
	payload, err := secure.Payload(value)
	if err != nil {
		return claims, errors.New("invalid agent token")
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return claims, errors.New("invalid agent token")
	}
	if claims.Kind != agentTokenKind {
		return claims, errors.New("invalid agent token kind")
	}
	if strings.TrimSpace(claims.Workspace) == "" || strings.TrimSpace(claims.Owner) == "" {
		return claims, errors.New("invalid agent token claims")
	}
	payload, err = secure.Verify(t.workspaceKey(claims.Workspace), value)
	if err != nil {
		return claims, errors.New("invalid agent token")
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return claims, errors.New("invalid agent token")
	}
	if claims.Expires < time.Now().Unix() {
		return claims, errors.New("agent token expired")
	}
	return claims, nil
}

// tokenExpiry returns the expiry encoded in a token without verifying it. The
// agent uses it only to choose the freshest credential it already trusts.
func tokenExpiry(value string) time.Time {
	value = strings.TrimSpace(value)
	payload, err := secure.Payload(value)
	if err != nil {
		return time.Time{}
	}
	var claims AgentClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return time.Time{}
	}
	if claims.Expires <= 0 {
		return time.Time{}
	}
	return time.Unix(claims.Expires, 0)
}

// workspaceKey derives a distinct signing key per workspace. A token minted
// for one workspace cannot be retargeted at another, even if the shared agent
// secret leaks from a single workspace.
func (t *TokenSigner) workspaceKey(workspace string) []byte {
	mac := hmac.New(sha256.New, t.key)
	_, _ = mac.Write([]byte("poddy-agent:" + strings.ToLower(strings.TrimSpace(workspace))))
	return mac.Sum(nil)
}
