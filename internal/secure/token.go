// Package secure provides the HMAC token helpers shared by authentication
// sessions, OAuth flows, and workspace tokens.
package secure

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
)

var errInvalidSignature = errors.New("invalid signature")

// Sign returns a URL-safe payload with its HMAC-SHA256 signature.
func Sign(key, payload []byte) string {
	mac := hmac.New(sha256.New, key)
	mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// Verify checks a signed value and returns the original payload.
func Verify(key []byte, value string) ([]byte, error) {
	parts := strings.Split(value, ".")
	if len(parts) != 2 {
		return nil, errInvalidSignature
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, errInvalidSignature
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, errInvalidSignature
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(payload)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return nil, errInvalidSignature
	}
	return payload, nil
}

// RandomString returns n cryptographically random bytes encoded for URLs.
func RandomString(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
