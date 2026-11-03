// Package userstore keeps the identities that have signed in through OIDC.
//
// It is a suggestion directory for the access and team editors, not an
// authentication source. Passwords and sessions stay with the OIDC provider.
package userstore

import (
	"context"
	"encoding/json"
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
)

const (
	// DefaultConfigMap is the ConfigMap used when no name is configured.
	DefaultConfigMap = "poddy-users"
	dataKey          = "users.json"
)

// User is an OIDC identity seen by the portal.
type User struct {
	Subject   string    `json:"subject,omitempty"`
	Username  string    `json:"username,omitempty"`
	Email     string    `json:"email,omitempty"`
	Name      string    `json:"name,omitempty"`
	Groups    []string  `json:"groups,omitempty"`
	FirstSeen time.Time `json:"firstSeen,omitempty"`
	LastSeen  time.Time `json:"lastSeen,omitempty"`
}

type document struct {
	Users map[string]User `json:"users"`
}

// Store persists users in a Kubernetes ConfigMap.
type Store struct {
	client    kubernetes.Interface
	namespace string
	name      string
	mu        sync.Mutex
}

// NewStore returns a ConfigMap-backed user directory.
func NewStore(client kubernetes.Interface, namespace, name string) *Store {
	if name == "" {
		name = DefaultConfigMap
	}
	return &Store{client: client, namespace: namespace, name: name}
}

// Load returns all stored users keyed by stable identity.
func (s *Store) Load(ctx context.Context) (map[string]User, error) {
	cm, err := s.client.CoreV1().ConfigMaps(s.namespace).Get(ctx, s.name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return map[string]User{}, nil
	}
	if err != nil {
		return nil, err
	}
	return decode(cm.Data[dataKey])
}

// List returns users sorted by email, username, then name.
func (s *Store) List(ctx context.Context) ([]User, error) {
	users, err := s.Load(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]User, 0, len(users))
	for _, user := range users {
		out = append(out, user)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Email != b.Email {
			return strings.ToLower(a.Email) < strings.ToLower(b.Email)
		}
		if a.Username != b.Username {
			return strings.ToLower(a.Username) < strings.ToLower(b.Username)
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
	return out, nil
}

// Upsert records a login. The first-seen timestamp is preserved.
func (s *Store) Upsert(ctx context.Context, user User) (User, error) {
	user.Subject = strings.TrimSpace(user.Subject)
	user.Username = strings.TrimSpace(user.Username)
	user.Email = strings.TrimSpace(user.Email)
	user.Name = strings.TrimSpace(user.Name)
	if key(user) == "" {
		return User{}, fmt.Errorf("user has no subject, email or username")
	}

	var saved User
	err := s.update(ctx, func(users map[string]User) error {
		key := key(user)
		if existing, ok := users[key]; ok {
			user.FirstSeen = existing.FirstSeen
			if user.Groups == nil {
				user.Groups = existing.Groups
			}
		}
		now := time.Now().UTC()
		if user.FirstSeen.IsZero() {
			user.FirstSeen = now
		}
		user.LastSeen = now
		users[key] = user
		saved = user
		return nil
	})
	return saved, err
}

// Search returns users whose email, username or name contains q.
func (s *Store) Search(ctx context.Context, q string, limit int) ([]User, error) {
	users, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	q = strings.ToLower(strings.TrimSpace(q))
	out := make([]User, 0, len(users))
	for _, user := range users {
		if q != "" {
			haystack := strings.ToLower(user.Email + " " + user.Username + " " + user.Name)
			if !strings.Contains(haystack, q) {
				continue
			}
		}
		out = append(out, user)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (s *Store) update(ctx context.Context, mutate func(map[string]User) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		cm, err := s.client.CoreV1().ConfigMaps(s.namespace).Get(ctx, s.name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			users := map[string]User{}
			if err := mutate(users); err != nil {
				return err
			}
			raw, err := encode(users)
			if err != nil {
				return err
			}
			_, err = s.client.CoreV1().ConfigMaps(s.namespace).Create(ctx, &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      s.name,
					Namespace: s.namespace,
					Labels:    map[string]string{"app.kubernetes.io/managed-by": "poddy"},
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
		users, err := decode(cm.Data[dataKey])
		if err != nil {
			return err
		}
		if err := mutate(users); err != nil {
			return err
		}
		raw, err := encode(users)
		if err != nil {
			return err
		}
		if cm.Data == nil {
			cm.Data = map[string]string{}
		}
		cm.Data[dataKey] = raw
		_, err = s.client.CoreV1().ConfigMaps(s.namespace).Update(ctx, cm, metav1.UpdateOptions{})
		return err
	})
}

func key(user User) string {
	switch {
	case user.Subject != "":
		return "sub:" + user.Subject
	case user.Email != "":
		return "email:" + strings.ToLower(user.Email)
	default:
		return "user:" + strings.ToLower(user.Username)
	}
}

func encode(users map[string]User) (string, error) {
	raw, err := json.Marshal(document{Users: users})
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func decode(raw string) (map[string]User, error) {
	if strings.TrimSpace(raw) == "" {
		return map[string]User{}, nil
	}
	var doc document
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return nil, fmt.Errorf("decode users: %w", err)
	}
	if doc.Users == nil {
		doc.Users = map[string]User{}
	}
	return doc.Users, nil
}
