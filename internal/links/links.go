// Package links defines the custom shortcuts embedded in workspace templates.
//
// URLs may contain placeholders such as {name}, {alias}, {sshHost} or
// {sshPort}; the portal substitutes them in the browser.
package links

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

var (
	iconName  = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,32}$`)
	linkName  = regexp.MustCompile(`^[^[:cntrl:]]{1,64}$`)
	scheme    = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*$`)
	badScheme = map[string]bool{
		"javascript": true,
		"data":       true,
		"vbscript":   true,
	}
)

// Link is one shortcut shown on workspace cards created from a template.
type Link struct {
	Name   string `json:"name" yaml:"name"`
	URL    string `json:"url" yaml:"url"`
	Icon   string `json:"icon,omitempty" yaml:"icon,omitempty"`
	NewTab bool   `json:"newTab,omitempty" yaml:"newTab,omitempty"`
}

// Normalize trims values and drops empty links.
func Normalize(values []Link) []Link {
	out := make([]Link, 0, len(values))
	for _, link := range values {
		link.Name = strings.TrimSpace(link.Name)
		link.URL = strings.TrimSpace(link.URL)
		link.Icon = strings.TrimSpace(link.Icon)
		if link.Name == "" && link.URL == "" {
			continue
		}
		if link.Icon == "" {
			link.Icon = "external"
		}
		out = append(out, link)
	}
	return out
}

// Validate checks names, icons and URLs. URLs may use {placeholders}.
func Validate(values []Link) error {
	seen := map[string]bool{}
	for _, link := range values {
		if !linkName.MatchString(link.Name) {
			return fmt.Errorf("invalid link name %q: use 1-64 printable characters", link.Name)
		}
		if seen[strings.ToLower(link.Name)] {
			return fmt.Errorf("duplicate link name %q", link.Name)
		}
		seen[strings.ToLower(link.Name)] = true

		if link.Icon != "" && !iconName.MatchString(link.Icon) {
			return fmt.Errorf("invalid icon %q: use letters, numbers, dashes or underscores", link.Icon)
		}
		if err := validateURL(link.URL); err != nil {
			return fmt.Errorf("link %q: %w", link.Name, err)
		}
	}
	return nil
}

func validateURL(raw string) error {
	if raw == "" {
		return fmt.Errorf("url is required")
	}
	if len(raw) > 2048 {
		return fmt.Errorf("url is too long")
	}
	for _, r := range raw {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("url contains a control character")
		}
	}
	if strings.ContainsAny(raw, " \t\r\n") {
		return fmt.Errorf("url must not contain whitespace")
	}
	if strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "#") || strings.HasPrefix(raw, "?") {
		return nil
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}
	if parsed.Scheme == "" {
		return fmt.Errorf("url must be absolute or start with /")
	}
	if !scheme.MatchString(parsed.Scheme) {
		return fmt.Errorf("invalid url scheme %q", parsed.Scheme)
	}
	if badScheme[strings.ToLower(parsed.Scheme)] {
		return fmt.Errorf("url scheme %q is not allowed", parsed.Scheme)
	}
	if (strings.EqualFold(parsed.Scheme, "http") || strings.EqualFold(parsed.Scheme, "https")) && parsed.Host == "" {
		return fmt.Errorf("url is missing a host")
	}
	return nil
}
