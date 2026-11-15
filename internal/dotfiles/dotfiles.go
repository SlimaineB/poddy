// Package dotfiles stores the per-user dotfiles repository copied into
// workspace homes at startup. A portal-wide fallback can be configured for
// users who have not set their own repository.
package dotfiles

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultBranch is used when a repository URL is configured without a
	// branch.
	DefaultBranch = "main"
	// SourceUser means the user configured their own repository.
	SourceUser = "user"
	// SourceGlobal means the portal fallback is in use.
	SourceGlobal = "global"
)

var (
	scpURL    = regexp.MustCompile(`^[A-Za-z0-9._-]+@[A-Za-z0-9.-]+:[^\s]+$`)
	branchRef = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,254}$`)
)

// Config is one dotfiles repository.
type Config struct {
	URL       string    `json:"url"`
	Branch    string    `json:"branch,omitempty"`
	UpdatedAt time.Time `json:"updatedAt,omitempty"`
}

// Empty reports whether no repository is configured.
func (c Config) Empty() bool {
	return strings.TrimSpace(c.URL) == ""
}

// Normalize trims and validates a config. An empty URL clears the branch.
func Normalize(cfg Config) (Config, error) {
	cfg.URL = strings.TrimSpace(cfg.URL)
	cfg.Branch = strings.TrimSpace(cfg.Branch)
	if cfg.URL == "" {
		cfg.Branch = ""
		return cfg, nil
	}
	if err := validateURL(cfg.URL); err != nil {
		return Config{}, err
	}
	if cfg.Branch == "" {
		cfg.Branch = DefaultBranch
	}
	if err := validateBranch(cfg.Branch); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func validateURL(raw string) error {
	if len(raw) > 2048 {
		return errors.New("dotfiles repository URL must be 2048 characters or less")
	}
	if strings.ContainsAny(raw, "\r\n\t ") {
		return errors.New("dotfiles repository URL must be a single line without spaces")
	}
	if strings.HasPrefix(raw, "-") {
		return errors.New("invalid dotfiles repository URL")
	}
	if !strings.Contains(raw, "://") {
		if !scpURL.MatchString(raw) {
			return errors.New("dotfiles repository must be an HTTPS, SSH, or git@host:path URL")
		}
		return nil
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid dotfiles repository URL: %w", err)
	}
	switch strings.ToLower(parsed.Scheme) {
	case "https", "ssh":
	default:
		return errors.New("dotfiles repository must use HTTPS or SSH")
	}
	if parsed.Hostname() == "" {
		return errors.New("dotfiles repository URL must include a host")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("dotfiles repository URL must not include a query or fragment")
	}
	if parsed.User != nil {
		if _, hasPassword := parsed.User.Password(); hasPassword {
			return errors.New("dotfiles repository URL must not include a password or token")
		}
		if strings.EqualFold(parsed.Scheme, "https") {
			return errors.New("dotfiles repository URL must not include credentials")
		}
	}
	if strings.Trim(parsed.Path, "/") == "" {
		return errors.New("dotfiles repository URL must include a repository path")
	}
	return nil
}

func validateBranch(branch string) error {
	if !branchRef.MatchString(branch) {
		return errors.New("invalid dotfiles branch")
	}
	if strings.Contains(branch, "..") || strings.Contains(branch, "//") ||
		strings.Contains(branch, "@{") || strings.Contains(branch, "\\") ||
		strings.HasSuffix(branch, "/") || strings.HasSuffix(branch, ".") ||
		strings.HasSuffix(branch, ".lock") {
		return errors.New("invalid dotfiles branch")
	}
	return nil
}

// NormalizeOwner canonicalizes the user identity used as the store key.
func NormalizeOwner(owner string) string {
	return strings.ToLower(strings.TrimSpace(owner))
}

// Store reads and writes per-user dotfiles settings for one namespace.
type Store interface {
	Get(ctx context.Context, owner string) (Config, error)
	Set(ctx context.Context, owner string, cfg Config) error
	Delete(ctx context.Context, owner string) error
}

// Effective resolves the user setting, falling back to the portal default.
func Effective(ctx context.Context, store Store, owner string, fallback Config) (Config, string, error) {
	if store != nil && NormalizeOwner(owner) != "" {
		cfg, err := store.Get(ctx, owner)
		if err != nil {
			return Config{}, "", err
		}
		if !cfg.Empty() {
			return cfg, SourceUser, nil
		}
	}
	if !fallback.Empty() {
		return fallback, SourceGlobal, nil
	}
	return Config{}, "", nil
}

// Memory is an in-process Store used by tests and local development.
type Memory struct {
	mu     sync.RWMutex
	config map[string]Config
}

// NewMemory returns an empty in-memory store.
func NewMemory() *Memory {
	return &Memory{config: map[string]Config{}}
}

func (m *Memory) Get(_ context.Context, owner string) (Config, error) {
	owner = NormalizeOwner(owner)
	if owner == "" {
		return Config{}, errors.New("owner is required")
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.config[owner], nil
}

func (m *Memory) Set(_ context.Context, owner string, cfg Config) error {
	owner = NormalizeOwner(owner)
	if owner == "" {
		return errors.New("owner is required")
	}
	cfg, err := Normalize(cfg)
	if err != nil {
		return err
	}
	if cfg.Empty() {
		return errors.New("dotfiles repository URL is required")
	}
	cfg.UpdatedAt = time.Now().UTC()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.config[owner] = cfg
	return nil
}

func (m *Memory) Delete(_ context.Context, owner string) error {
	owner = NormalizeOwner(owner)
	if owner == "" {
		return errors.New("owner is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.config, owner)
	return nil
}
