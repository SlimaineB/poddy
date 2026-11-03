// Package sshkeys parses authorized_keys entries and maps keys to workspace
// owners. The key comment carries the owner identity:
//
//   - "user:alice@example.com" or "alice@example.com" -> owner alice@example.com
//   - "user:token" -> owner token (token auth has no email)
//   - "admin" or "*" -> bastion administrator (all workspaces)
//
// Keys without a recognizable comment are denied by default. This prevents a
// shared authorized_keys file from granting access to every workspace.
package sshkeys

import (
	"fmt"
	"strings"

	"golang.org/x/crypto/ssh"
)

// Key is one parsed authorized_keys entry.
type Key struct {
	Line        string
	Fingerprint string
	User        string
	Admin       bool
}

// Set is an immutable keyring built from authorized_keys lines.
type Set struct {
	keys          []Key
	byFingerprint map[string]Key
	allowUnmapped bool
}

// Parse builds a keyring. When allowUnmapped is true, keys without an owner
// comment keep the legacy behavior and can access every workspace.
func Parse(lines []string, allowUnmapped bool) (*Set, error) {
	set := &Set{byFingerprint: map[string]Key{}, allowUnmapped: allowUnmapped}
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		public, comment, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
		if err != nil {
			return nil, fmt.Errorf("invalid authorized key: %w", err)
		}
		fingerprint := ssh.FingerprintSHA256(public)
		user, admin := parseComment(comment)
		key := Key{Line: line, Fingerprint: fingerprint, User: user, Admin: admin}
		set.keys = append(set.keys, key)
		set.byFingerprint[fingerprint] = key
	}
	return set, nil
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
	out := make([]string, 0, len(s.keys))
	for _, key := range s.keys {
		if s.allowUnmapped || key.Admin || (key.User != "" && strings.EqualFold(key.User, owner)) {
			out = append(out, key.Line)
		}
	}
	return out
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
