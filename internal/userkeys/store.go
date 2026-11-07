// Package userkeys stores self-service SSH public keys in a ConfigMap.
//
// Keys are bound to the authenticated identity by the portal. The key comment
// is never trusted for authorization: the owner comes from the registry entry.
package userkeys

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"

	"poddy/internal/sshkeys"
)

var (
	// ErrDuplicate is returned when the same public key is already registered.
	ErrDuplicate = errors.New("SSH key is already registered")
	// ErrLimit is returned when an owner has too many keys.
	ErrLimit = errors.New("maximum number of SSH keys reached")
	// ErrNotFound is returned when a key does not exist for the owner.
	ErrNotFound = errors.New("SSH key not found")
)

const (
	// DefaultConfigMap is the ConfigMap used when no name is configured.
	DefaultConfigMap = "poddy-user-ssh-keys"
	// Label marks the ConfigMap managed by this store.
	Label = "poddy.io/ssh-user-keys"
	// MaxKeysPerOwner limits the damage a compromised account can cause and
	// keeps the ConfigMap below the Kubernetes object size limit.
	MaxKeysPerOwner = 10
	dataKey         = "keys.json"
)

// Key is one registered public key.
type Key struct {
	Owner       string    `json:"owner"`
	Line        string    `json:"line"`
	Fingerprint string    `json:"fingerprint"`
	ID          string    `json:"id"`
	Type        string    `json:"type"`
	AddedAt     time.Time `json:"addedAt"`
}

type document struct {
	Keys []Key `json:"keys"`
}

// Store persists user SSH keys in a ConfigMap.
type Store struct {
	client    kubernetes.Interface
	namespace string
	name      string
	mu        sync.Mutex
}

// NewStore returns a ConfigMap-backed key registry.
func NewStore(client kubernetes.Interface, namespace, name string) *Store {
	if name == "" {
		name = DefaultConfigMap
	}
	return &Store{client: client, namespace: namespace, name: name}
}

// Name returns the ConfigMap name.
func (s *Store) Name() string { return s.name }

// List returns all registered keys sorted by owner, then fingerprint.
func (s *Store) List(ctx context.Context) ([]Key, error) {
	cm, err := s.client.CoreV1().ConfigMaps(s.namespace).Get(ctx, s.name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return []Key{}, nil
	}
	if err != nil {
		return nil, err
	}
	return decode(cm.Data[dataKey])
}

// ListForOwner returns keys owned by owner.
func (s *Store) ListForOwner(ctx context.Context, owner string) ([]Key, error) {
	keys, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	owner = normalizeOwner(owner)
	out := make([]Key, 0)
	for _, key := range keys {
		if key.Owner == owner {
			out = append(out, key)
		}
	}
	return out, nil
}

// Add validates and stores a public key for owner. The key comment is ignored
// and replaced with a canonical owner comment.
func (s *Store) Add(ctx context.Context, owner, line string) (Key, error) {
	owner = normalizeOwner(owner)
	if owner == "" {
		return Key{}, fmt.Errorf("owner is required")
	}
	normalized, fingerprint, id, keyType, err := sshkeys.ParsePublicKey(line)
	if err != nil {
		return Key{}, err
	}

	var saved Key
	err = s.update(ctx, func(keys []Key) ([]Key, error) {
		perOwner := 0
		for _, key := range keys {
			if key.Fingerprint == fingerprint {
				if key.Owner == owner {
					return keys, ErrDuplicate
				}
				return keys, ErrDuplicate
			}
			if key.Owner == owner {
				perOwner++
			}
		}
		if perOwner >= MaxKeysPerOwner {
			return keys, fmt.Errorf("%w (%d)", ErrLimit, MaxKeysPerOwner)
		}
		saved = Key{
			Owner:       owner,
			Line:        normalized + " user:" + owner,
			Fingerprint: fingerprint,
			ID:          id,
			Type:        keyType,
			AddedAt:     time.Now().UTC(),
		}
		return append(keys, saved), nil
	})
	return saved, err
}

// Remove deletes a key by its stable ID. A key belonging to another owner is
// reported as not found, never deleted.
func (s *Store) Remove(ctx context.Context, owner, id string) error {
	owner = normalizeOwner(owner)
	id = strings.TrimSpace(id)
	if owner == "" || id == "" {
		return fmt.Errorf("owner and key id are required")
	}
	return s.update(ctx, func(keys []Key) ([]Key, error) {
		for i, key := range keys {
			if key.ID == id && key.Owner == owner {
				return append(keys[:i], keys[i+1:]...), nil
			}
		}
		return keys, ErrNotFound
	})
}

// Owned converts registry keys to the owner-bound input expected by sshkeys.
func Owned(keys []Key) []sshkeys.OwnedKey {
	out := make([]sshkeys.OwnedKey, 0, len(keys))
	for _, key := range keys {
		out = append(out, sshkeys.OwnedKey{Owner: key.Owner, Line: key.Line})
	}
	return out
}

func (s *Store) update(ctx context.Context, mutate func([]Key) ([]Key, error)) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		cm, err := s.client.CoreV1().ConfigMaps(s.namespace).Get(ctx, s.name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			keys, err := mutate(nil)
			if err != nil {
				return err
			}
			raw, err := encode(keys)
			if err != nil {
				return err
			}
			_, err = s.client.CoreV1().ConfigMaps(s.namespace).Create(ctx, &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      s.name,
					Namespace: s.namespace,
					Labels: map[string]string{
						"app.kubernetes.io/managed-by": "poddy",
						Label:                          "true",
					},
				},
				Data: map[string]string{dataKey: raw},
			}, metav1.CreateOptions{})
			if apierrors.IsAlreadyExists(err) {
				return apierrors.NewConflict(corev1.Resource("configmaps"), s.name, err)
			}
			return err
		}
		if err != nil {
			return err
		}
		keys, err := decode(cm.Data[dataKey])
		if err != nil {
			return err
		}
		keys, err = mutate(keys)
		if err != nil {
			return err
		}
		raw, err := encode(keys)
		if err != nil {
			return err
		}
		if cm.Data == nil {
			cm.Data = map[string]string{}
		}
		cm.Data[dataKey] = raw
		if cm.Labels == nil {
			cm.Labels = map[string]string{}
		}
		cm.Labels["app.kubernetes.io/managed-by"] = "poddy"
		cm.Labels[Label] = "true"
		_, err = s.client.CoreV1().ConfigMaps(s.namespace).Update(ctx, cm, metav1.UpdateOptions{})
		return err
	})
}

func normalizeOwner(owner string) string {
	return strings.ToLower(strings.TrimSpace(owner))
}

func encode(keys []Key) (string, error) {
	if keys == nil {
		keys = []Key{}
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Owner != keys[j].Owner {
			return keys[i].Owner < keys[j].Owner
		}
		return keys[i].Fingerprint < keys[j].Fingerprint
	})
	raw, err := json.Marshal(document{Keys: keys})
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func decode(raw string) ([]Key, error) {
	if strings.TrimSpace(raw) == "" {
		return []Key{}, nil
	}
	var doc document
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return nil, fmt.Errorf("decode SSH user keys: %w", err)
	}
	if doc.Keys == nil {
		doc.Keys = []Key{}
	}
	return doc.Keys, nil
}
