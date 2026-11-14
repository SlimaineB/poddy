// Package secure provides the HMAC token helpers shared by authentication
// sessions, OAuth flows, and workspace tokens.
package secure

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

var (
	errInvalidSignature = errors.New("invalid signature")
	errNoCipherKey      = errors.New("encryption key is empty")
	errInvalidCipher    = errors.New("invalid encrypted value")
)

// Key derives a stable 32-byte AES key from an application secret.
func Key(secret string) []byte {
	sum := sha256.Sum256([]byte(secret))
	return sum[:]
}

// Encrypt seals plaintext with AES-GCM. The returned value is nonce||ciphertext.
func Encrypt(key, plaintext []byte) ([]byte, error) {
	if len(key) == 0 {
		return nil, errNoCipherKey
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("encryption key: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("encryption cipher: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("encryption nonce: %w", err)
	}
	out := make([]byte, 0, len(nonce)+len(plaintext)+gcm.Overhead())
	out = append(out, nonce...)
	return gcm.Seal(out, nonce, plaintext, nil), nil
}

// Decrypt opens a value produced by Encrypt.
func Decrypt(key, ciphertext []byte) ([]byte, error) {
	if len(key) == 0 {
		return nil, errNoCipherKey
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("encryption key: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("encryption cipher: %w", err)
	}
	if len(ciphertext) < gcm.NonceSize() {
		return nil, errInvalidCipher
	}
	nonce, body := ciphertext[:gcm.NonceSize()], ciphertext[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, body, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt value: %w", err)
	}
	return plaintext, nil
}

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
