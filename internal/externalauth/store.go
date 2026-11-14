package externalauth

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"
)

const secretKey = "tokens"

// Token is one user's OAuth2 credentials for a provider.
type Token struct {
	AccessToken  string    `json:"accessToken"`
	RefreshToken string    `json:"refreshToken,omitempty"`
	TokenType    string    `json:"tokenType,omitempty"`
	Expiry       time.Time `json:"expiry,omitempty"`
	Scopes       []string  `json:"scopes,omitempty"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

// Expired reports whether the access token should be refreshed.
func (t Token) Expired(now time.Time) bool {
	if t.AccessToken == "" {
		return true
	}
	if t.Expiry.IsZero() {
		return false
	}
	return !t.Expiry.After(now.Add(time.Minute))
}

type tokenMap map[string]map[string]Token

// Store persists tokens in a Kubernetes Secret. The JSON document is encrypted
// with AES-GCM when a key is configured.
type Store struct {
	client    kubernetes.Interface
	namespace string
	name      string
	key       []byte
	mu        sync.Mutex
}

// NewStore returns a token store. secret is hashed into a 32-byte AES key; an
// empty secret disables encryption (not recommended in production).
func NewStore(client kubernetes.Interface, namespace, name, secret string) *Store {
	var key []byte
	if secret != "" {
		sum := sha256.Sum256([]byte(secret))
		key = sum[:]
	}
	if name == "" {
		name = "poddy-external-auth"
	}
	return &Store{client: client, namespace: namespace, name: name, key: key}
}

// Get returns the stored token for a user and provider.
func (s *Store) Get(ctx context.Context, user, provider string) (Token, bool, error) {
	data, err := s.load(ctx)
	if err != nil {
		return Token{}, false, err
	}
	token, ok := data[user][provider]
	return token, ok, nil
}

// Put stores or replaces a token.
func (s *Store) Put(ctx context.Context, user, provider string, token Token) error {
	return s.update(ctx, func(data tokenMap) error {
		if data[user] == nil {
			data[user] = map[string]Token{}
		}
		token.UpdatedAt = time.Now().UTC()
		data[user][provider] = token
		return nil
	})
}

// Delete removes a user's token for one provider.
func (s *Store) Delete(ctx context.Context, user, provider string) error {
	return s.update(ctx, func(data tokenMap) error {
		if data[user] == nil {
			return nil
		}
		delete(data[user], provider)
		if len(data[user]) == 0 {
			delete(data, user)
		}
		return nil
	})
}

// All returns a copy of every stored token. It is used to seed PostgreSQL
// during an upgrade.
func (s *Store) All(ctx context.Context) (map[string]map[string]Token, error) {
	data, err := s.load(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]map[string]Token, len(data))
	for user, providers := range data {
		out[user] = make(map[string]Token, len(providers))
		for provider, token := range providers {
			out[user][provider] = token
		}
	}
	return out, nil
}

func (s *Store) update(ctx context.Context, mutate func(tokenMap) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		secret, err := s.client.CoreV1().Secrets(s.namespace).Get(ctx, s.name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			data := tokenMap{}
			if err := mutate(data); err != nil {
				return err
			}
			raw, err := s.encode(data)
			if err != nil {
				return err
			}
			_, err = s.client.CoreV1().Secrets(s.namespace).Create(ctx, &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      s.name,
					Namespace: s.namespace,
					Labels:    map[string]string{"app.kubernetes.io/managed-by": "poddy"},
				},
				Type: corev1.SecretTypeOpaque,
				Data: map[string][]byte{secretKey: raw},
			}, metav1.CreateOptions{})
			return err
		}
		if err != nil {
			return err
		}

		data, err := s.decode(secret.Data[secretKey])
		if err != nil {
			return err
		}
		if err := mutate(data); err != nil {
			return err
		}
		raw, err := s.encode(data)
		if err != nil {
			return err
		}
		if secret.Data == nil {
			secret.Data = map[string][]byte{}
		}
		secret.Data[secretKey] = raw
		_, err = s.client.CoreV1().Secrets(s.namespace).Update(ctx, secret, metav1.UpdateOptions{})
		return err
	})
}

func (s *Store) load(ctx context.Context) (tokenMap, error) {
	secret, err := s.client.CoreV1().Secrets(s.namespace).Get(ctx, s.name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return tokenMap{}, nil
	}
	if err != nil {
		return nil, err
	}
	return s.decode(secret.Data[secretKey])
}

func (s *Store) encode(data tokenMap) ([]byte, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	if len(s.key) == 0 {
		return append([]byte("plain:"), raw...), nil
	}
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	out := append([]byte("v1:"), nonce...)
	return gcm.Seal(out, nonce, raw, nil), nil
}

func (s *Store) decode(raw []byte) (tokenMap, error) {
	if len(raw) == 0 {
		return tokenMap{}, nil
	}
	payload := raw
	if len(raw) >= 3 && string(raw[:3]) == "v1:" {
		if len(s.key) == 0 {
			return nil, fmt.Errorf("external auth token store is encrypted but no key is configured")
		}
		block, err := aes.NewCipher(s.key)
		if err != nil {
			return nil, err
		}
		gcm, err := cipher.NewGCM(block)
		if err != nil {
			return nil, err
		}
		body := raw[3:]
		if len(body) < gcm.NonceSize() {
			return nil, fmt.Errorf("invalid encrypted token store")
		}
		nonce, ciphertext := body[:gcm.NonceSize()], body[gcm.NonceSize():]
		payload, err = gcm.Open(nil, nonce, ciphertext, nil)
		if err != nil {
			return nil, fmt.Errorf("decrypt token store: %w", err)
		}
	} else if len(raw) >= 6 && string(raw[:6]) == "plain:" {
		payload = raw[6:]
	} else {
		return nil, fmt.Errorf("unknown token store format")
	}

	data := tokenMap{}
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &data); err != nil {
			return nil, fmt.Errorf("decode token store: %w", err)
		}
	}
	return data, nil
}
