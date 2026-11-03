package app

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"poddy/internal/local"
)

func collectKeys(editor *local.Local, raw, file string) ([]string, error) {
	var keys []string
	if editor != nil {
		for _, key := range editor.AuthorizedKeys() {
			keys = append(keys, trustedKey(key))
		}
	}
	for _, key := range strings.Split(raw, ",") {
		if key = strings.TrimSpace(key); key != "" {
			keys = append(keys, key)
		}
	}
	if file != "" {
		data, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			keys = append(keys, line)
		}
	}

	seen := make(map[string]bool, len(keys))
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, key)
	}
	return out, nil
}

// trustedKey marks a key managed by the local editor as an admin key. Local
// mode runs on the operator's machine and must keep working with token or OIDC
// auth, where an uncommented key would otherwise be rejected.
func trustedKey(line string) string {
	public, _, _, _, err := ssh.ParseAuthorizedKey([]byte(strings.TrimSpace(line)))
	if err != nil {
		return line
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(public))) + " admin"
}

func randomSecret() string {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return base64.RawURLEncoding.EncodeToString(buf)
}

func splitList(value string) []string {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ' '
	})
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		if field = strings.TrimSpace(field); field != "" {
			out = append(out, field)
		}
	}
	return out
}
