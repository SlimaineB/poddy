package k8s

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"
)

const (
	DefaultSharesSecret = "poddy-shares"
	sharesDataKey       = "shares.json"

	ShareAccessInternal = "internal"
	ShareAccessPublic   = "public"
)

type Share struct {
	Token     string    `json:"token"`
	Workspace string    `json:"workspace"`
	Port      int       `json:"port,omitempty"`
	Path      string    `json:"path"`
	Access    string    `json:"access"`
	CreatedBy string    `json:"createdBy,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type ShareStore struct {
	client    kubernetes.Interface
	namespace string
	name      string
}

func NewShareStore(client kubernetes.Interface, namespace, name string) *ShareStore {
	if name == "" {
		name = DefaultSharesSecret
	}
	return &ShareStore{client: client, namespace: namespace, name: name}
}

func NewShareToken() (string, error) {
	var data [32]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", fmt.Errorf("generate share token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(data[:]), nil
}

func (s *ShareStore) Load(ctx context.Context) (map[string]Share, error) {
	secret, err := s.client.CoreV1().Secrets(s.namespace).Get(ctx, s.name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return map[string]Share{}, nil
	}
	if err != nil {
		return nil, err
	}
	shares := map[string]Share{}
	if raw := strings.TrimSpace(string(secret.Data[sharesDataKey])); raw != "" {
		if err := json.Unmarshal([]byte(raw), &shares); err != nil {
			return nil, fmt.Errorf("parse shares: %w", err)
		}
	}
	if shares == nil {
		shares = map[string]Share{}
	}
	return shares, nil
}

func (s *ShareStore) GetByToken(ctx context.Context, token string) (Share, bool, error) {
	shares, err := s.Load(ctx)
	if err != nil {
		return Share{}, false, err
	}
	share, ok := shares[token]
	return share, ok, nil
}

func (s *ShareStore) GetByTarget(ctx context.Context, workspace, path string) (Share, bool, error) {
	shares, err := s.Load(ctx)
	if err != nil {
		return Share{}, false, err
	}
	for _, share := range shares {
		if share.Workspace == workspace && share.Path == path {
			return share, true, nil
		}
	}
	return Share{}, false, nil
}

func (s *ShareStore) Set(ctx context.Context, share Share) error {
	return s.Update(ctx, func(shares map[string]Share) error {
		for token, existing := range shares {
			if existing.Workspace == share.Workspace && existing.Path == share.Path && token != share.Token {
				delete(shares, token)
			}
		}
		shares[share.Token] = share
		return nil
	})
}

func (s *ShareStore) DeleteTarget(ctx context.Context, workspace, path string) error {
	shares, err := s.Load(ctx)
	if err != nil {
		return err
	}
	found := false
	for _, share := range shares {
		if share.Workspace == workspace && share.Path == path {
			found = true
			break
		}
	}
	if !found {
		return nil
	}
	return s.Update(ctx, func(shares map[string]Share) error {
		for token, share := range shares {
			if share.Workspace == workspace && share.Path == path {
				delete(shares, token)
			}
		}
		return nil
	})
}

func (s *ShareStore) DeleteWorkspace(ctx context.Context, workspace string) error {
	shares, err := s.Load(ctx)
	if err != nil {
		return err
	}
	found := false
	for _, share := range shares {
		if share.Workspace == workspace {
			found = true
			break
		}
	}
	if !found {
		return nil
	}
	return s.Update(ctx, func(shares map[string]Share) error {
		for token, share := range shares {
			if share.Workspace == workspace {
				delete(shares, token)
			}
		}
		return nil
	})
}

func (s *ShareStore) Update(ctx context.Context, mutate func(map[string]Share) error) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		secret, err := s.client.CoreV1().Secrets(s.namespace).Get(ctx, s.name, metav1.GetOptions{})
		notFound := apierrors.IsNotFound(err)
		shares := map[string]Share{}
		if err == nil {
			if raw := strings.TrimSpace(string(secret.Data[sharesDataKey])); raw != "" {
				if err := json.Unmarshal([]byte(raw), &shares); err != nil {
					return fmt.Errorf("parse shares: %w", err)
				}
			}
		} else if !notFound {
			return err
		}
		if shares == nil {
			shares = map[string]Share{}
		}
		if err := mutate(shares); err != nil {
			return err
		}
		raw, err := json.Marshal(shares)
		if err != nil {
			return fmt.Errorf("marshal shares: %w", err)
		}
		if notFound {
			_, err = s.client.CoreV1().Secrets(s.namespace).Create(ctx, &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: s.name},
				Type:       corev1.SecretTypeOpaque,
				Data:       map[string][]byte{sharesDataKey: raw},
			}, metav1.CreateOptions{})
			if apierrors.IsAlreadyExists(err) {
				return apierrors.NewConflict(corev1.Resource("secrets"), s.name, err)
			}
			return err
		}
		if secret.Data == nil {
			secret.Data = map[string][]byte{}
		}
		secret.Data[sharesDataKey] = raw
		_, err = s.client.CoreV1().Secrets(s.namespace).Update(ctx, secret, metav1.UpdateOptions{})
		return err
	})
}
