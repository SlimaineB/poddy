package region

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"poddy/internal/auth"
)

const (
	IdentityHeader   = "X-Poddy-Region-Identity"
	RegionNameHeader = "X-Poddy-Region-Name"
	identityTTL      = 2 * time.Minute
)

var ErrInvalidIdentity = errors.New("invalid region identity")

type Identity struct {
	User    auth.User `json:"user"`
	Admin   bool      `json:"admin"`
	Region  string    `json:"region,omitempty"`
	Expires int64     `json:"exp,omitempty"`
}

func SignIdentity(key string, identity Identity) (string, error) {
	if strings.TrimSpace(key) == "" {
		return "", errors.New("region key is empty")
	}
	if identity.Expires == 0 {
		identity.Expires = time.Now().Add(identityTTL).Unix()
	}
	payload, err := json.Marshal(identity)
	if err != nil {
		return "", err
	}
	body := base64.RawURLEncoding.EncodeToString(payload)
	return body + "." + sign(key, body), nil
}

func VerifyIdentity(key, token string) (Identity, error) {
	var identity Identity
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return identity, ErrInvalidIdentity
	}
	got, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(got) == 0 || !hmac.Equal(got, rawMAC(key, parts[0])) {
		return identity, ErrInvalidIdentity
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return identity, ErrInvalidIdentity
	}
	if err := json.Unmarshal(payload, &identity); err != nil {
		return identity, ErrInvalidIdentity
	}
	if identity.Expires == 0 || time.Now().After(time.Unix(identity.Expires, 0)) {
		return identity, ErrInvalidIdentity
	}
	if identity.User.Username == "" && identity.User.Email == "" && identity.User.Subject == "" {
		return identity, ErrInvalidIdentity
	}
	return identity, nil
}

func sign(key, body string) string {
	return base64.RawURLEncoding.EncodeToString(rawMAC(key, body))
}

func rawMAC(key, body string) []byte {
	mac := hmac.New(sha256.New, []byte(key))
	_, _ = mac.Write([]byte(body))
	return mac.Sum(nil)
}
