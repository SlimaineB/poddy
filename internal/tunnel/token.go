package tunnel

import (
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
	defaultTokenTTL = 7 * 24 * time.Hour
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
		sum := sha256.Sum256([]byte(secret))
		key = sum[:]
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
	return secure.Sign(t.key, payload), expires, nil
}

func (t *TokenSigner) VerifyAgent(value string) (AgentClaims, error) {
	var claims AgentClaims
	payload, err := secure.Verify(t.key, strings.TrimSpace(value))
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
	if claims.Expires < time.Now().Unix() {
		return claims, errors.New("agent token expired")
	}
	return claims, nil
}
