// Package sshkeys parses authorized_keys entries and maps keys to workspace
// owners. For administrator-managed keys the key comment carries the owner
// identity:
//
//   - "user:alice@example.com" or "alice@example.com" -> owner alice@example.com
//   - "user:token" -> owner token (token auth has no email)
//   - "admin" or "*" -> bastion administrator (all workspaces)
//
// Self-service keys are different: their owner is supplied explicitly by the
// portal and never read from the key comment. This prevents a user from
// claiming another identity by registering a key with a forged comment.
//
// Keys without a recognizable comment are denied by default. This prevents a
// shared authorized_keys file from granting access to every workspace.
package sshkeys

import (
	"crypto/rsa"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"

	"golang.org/x/crypto/ssh"
)

// Key is one parsed authorized_keys entry.
type Key struct {
	Line        string
	Fingerprint string
	ID          string
	Type        string
	Comment     string
	User        string
	Admin       bool
}

// OwnedKey is a key whose owner is assigned by the portal rather than by the
// key comment. It is used for self-service SSH keys.
type OwnedKey struct {
	Owner string
	Line  string
}

// Set is an immutable keyring built from authorized_keys lines.
type Set struct {
	keys          []Key
	byFingerprint map[string]Key
	allowUnmapped bool
}

// Keyring is the read-only interface used by the bastion and workspace
// manager. Implementations may be immutable (Set) or dynamically reloaded
// (Dynamic).
type Keyring interface {
	Len() int
	Lookup(fingerprint string) (Key, bool)
	Authorize(fingerprint, owner string) bool
	ForOwner(owner string) []string
}

// Parse builds a keyring from administrator-managed authorized_keys lines.
func Parse(lines []string, allowUnmapped bool) (*Set, error) {
	return Build(lines, nil, allowUnmapped)
}

// Build builds a keyring from administrator-managed lines and self-service
// keys. Administrator lines keep their comment-based identity. Owned keys are
// bound to OwnedKey.Owner and their comments are ignored.
func Build(adminLines []string, owned []OwnedKey, allowUnmapped bool) (*Set, error) {
	set := &Set{
		byFingerprint: map[string]Key{},
		allowUnmapped: allowUnmapped,
	}
	for _, line := range adminLines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		public, comment, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
		if err != nil {
			return nil, fmt.Errorf("invalid authorized key: %w", err)
		}
		if err := validatePublicKey(public); err != nil {
			return nil, err
		}
		user, admin := parseComment(comment)
		key := newKey(line, public, comment, user, admin)
		if err := set.add(key); err != nil {
			return nil, err
		}
	}

	for _, item := range owned {
		owner := strings.ToLower(strings.TrimSpace(item.Owner))
		if owner == "" {
			return nil, fmt.Errorf("self-service key has no owner")
		}
		line := strings.TrimSpace(item.Line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		public, _, options, _, err := ssh.ParseAuthorizedKey([]byte(line))
		if err != nil {
			return nil, fmt.Errorf("invalid self-service key: %w", err)
		}
		if len(options) > 0 {
			return nil, fmt.Errorf("self-service keys must not contain authorized_keys options")
		}
		if err := validatePublicKey(public); err != nil {
			return nil, err
		}
		normalized := NormalizePublicKey(public)
		key := newKey(normalized, public, "user:"+owner, owner, false)
		if err := set.add(key); err != nil {
			return nil, err
		}
	}
	return set, nil
}

func newKey(line string, public ssh.PublicKey, comment, user string, admin bool) Key {
	sum := sha256.Sum256(public.Marshal())
	return Key{
		Line:        line,
		Fingerprint: ssh.FingerprintSHA256(public),
		ID:          hex.EncodeToString(sum[:]),
		Type:        public.Type(),
		Comment:     strings.TrimSpace(comment),
		User:        strings.ToLower(strings.TrimSpace(user)),
		Admin:       admin,
	}
}

func (s *Set) add(key Key) error {
	if existing, ok := s.byFingerprint[key.Fingerprint]; ok {
		if !strings.EqualFold(existing.User, key.User) || existing.Admin != key.Admin {
			return fmt.Errorf("SSH key %s is assigned to more than one identity", key.Fingerprint)
		}
		return nil
	}
	s.keys = append(s.keys, key)
	s.byFingerprint[key.Fingerprint] = key
	return nil
}

// NormalizePublicKey returns the canonical authorized_keys representation of a
// public key without a comment.
func NormalizePublicKey(public ssh.PublicKey) string {
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(public)))
}

// ParsePublicKey validates a user-supplied public key and returns its
// canonical representation, OpenSSH fingerprint and stable identifier.
func ParsePublicKey(line string) (normalized, fingerprint, id, keyType string, err error) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", "", "", fmt.Errorf("public key is required")
	}
	if strings.ContainsAny(line, "\r\n") {
		return "", "", "", "", fmt.Errorf("public key must be a single line")
	}
	public, _, options, _, parseErr := ssh.ParseAuthorizedKey([]byte(line))
	if parseErr != nil {
		return "", "", "", "", fmt.Errorf("invalid public key: %w", parseErr)
	}
	if len(options) > 0 {
		return "", "", "", "", fmt.Errorf("public key must not contain authorized_keys options")
	}
	if err := validatePublicKey(public); err != nil {
		return "", "", "", "", err
	}
	key := newKey(NormalizePublicKey(public), public, "", "", false)
	return key.Line, key.Fingerprint, key.ID, key.Type, nil
}

func validatePublicKey(public ssh.PublicKey) error {
	if public == nil {
		return fmt.Errorf("invalid public key")
	}
	if public.Type() == ssh.KeyAlgoDSA {
		return fmt.Errorf("DSA keys are not supported")
	}
	if cryptoKey, ok := public.(ssh.CryptoPublicKey); ok {
		if rsaKey, ok := cryptoKey.CryptoPublicKey().(*rsa.PublicKey); ok && rsaKey.N.BitLen() < 3072 {
			return fmt.Errorf("RSA keys must be at least 3072 bits")
		}
	}
	return nil
}

// Len returns the number of parsed keys.
func (s *Set) Len() int {
	if s == nil {
		return 0
	}
	return len(s.keys)
}

// Lookup returns the key matching an SSH fingerprint.
func (s *Set) Lookup(fingerprint string) (Key, bool) {
	if s == nil {
		return Key{}, false
	}
	key, ok := s.byFingerprint[fingerprint]
	return key, ok
}

// Authorize reports whether a key may access a workspace owned by owner.
func (s *Set) Authorize(fingerprint, owner string) bool {
	key, ok := s.Lookup(fingerprint)
	if !ok {
		return false
	}
	if key.Admin {
		return true
	}
	if key.User == "" {
		return s.allowUnmapped
	}
	return strings.EqualFold(key.User, owner)
}

// ForOwner returns the authorized_keys lines that may be injected into a
// workspace owned by owner. Administrator keys are included for break-glass
// access.
func (s *Set) ForOwner(owner string) []string {
	if s == nil {
		return nil
	}
	owner = strings.ToLower(strings.TrimSpace(owner))
	out := make([]string, 0, len(s.keys))
	for _, key := range s.keys {
		if s.allowUnmapped || key.Admin || (key.User != "" && key.User == owner) {
			out = append(out, key.Line)
		}
	}
	return out
}

// Dynamic is a keyring that can be reloaded atomically while the bastion is
// serving connections.
type Dynamic struct {
	mu  sync.RWMutex
	set *Set
}

// NewDynamic returns a reloadable keyring.
func NewDynamic(adminLines []string, owned []OwnedKey, allowUnmapped bool) (*Dynamic, error) {
	set, err := Build(adminLines, owned, allowUnmapped)
	if err != nil {
		return nil, err
	}
	return &Dynamic{set: set}, nil
}

// Update replaces the keyring. The previous keyring stays active if the new
// one is invalid.
func (d *Dynamic) Update(adminLines []string, owned []OwnedKey, allowUnmapped bool) error {
	set, err := Build(adminLines, owned, allowUnmapped)
	if err != nil {
		return err
	}
	d.mu.Lock()
	d.set = set
	d.mu.Unlock()
	return nil
}

func (d *Dynamic) current() *Set {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.set
}

// Len returns the number of keys in the current keyring.
func (d *Dynamic) Len() int { return d.current().Len() }

// Lookup returns the key matching an SSH fingerprint.
func (d *Dynamic) Lookup(fingerprint string) (Key, bool) {
	return d.current().Lookup(fingerprint)
}

// Authorize reports whether a key may access a workspace owned by owner.
func (d *Dynamic) Authorize(fingerprint, owner string) bool {
	return d.current().Authorize(fingerprint, owner)
}

// ForOwner returns the authorized_keys lines for owner.
func (d *Dynamic) ForOwner(owner string) []string {
	return d.current().ForOwner(owner)
}

func parseComment(comment string) (string, bool) {
	comment = strings.TrimSpace(comment)
	lower := strings.ToLower(comment)
	switch lower {
	case "*", "admin", "poddy-admin":
		return "", true
	}
	if strings.HasPrefix(lower, "admin:") {
		return strings.TrimSpace(comment[len("admin:"):]), true
	}
	if strings.HasPrefix(lower, "user:") {
		return strings.TrimSpace(comment[len("user:"):]), false
	}
	if strings.Contains(comment, "@") {
		return comment, false
	}
	return "", false
}
