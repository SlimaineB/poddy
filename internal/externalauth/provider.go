package externalauth

import (
	"fmt"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Provider describes one OAuth2 application configured on the portal.
type Provider struct {
	ID            string
	Type          string
	DisplayName   string
	DisplayIcon   string
	ClientID      string
	ClientSecret  string
	AuthURL       string
	TokenURL      string
	ValidateURL   string
	RevokeURL     string
	RedirectURL   string
	Scopes        []string
	Regex         string
	Username      string
	Env           []string
	PKCE          bool
	compiledRegex *regexp.Regexp
}

// LoadProviders reads PODDY_EXTERNAL_AUTH_<n>_* variables from the process
// environment. Indices may be sparse and are sorted numerically.
func LoadProviders() ([]Provider, error) {
	return ParseProviders(os.Environ())
}

// ParseProviders parses Coder-style indexed environment variables.
func ParseProviders(environ []string) ([]Provider, error) {
	values := map[string]string{}
	for _, item := range environ {
		key, value, ok := strings.Cut(item, "=")
		if !ok {
			continue
		}
		values[key] = value
	}

	indices := map[int]bool{}
	re := regexp.MustCompile(`^PODDY_EXTERNAL_AUTH_([0-9]+)_ID$`)
	for key := range values {
		if match := re.FindStringSubmatch(key); match != nil {
			index, _ := strconv.Atoi(match[1])
			indices[index] = true
		}
	}
	ordered := make([]int, 0, len(indices))
	for index := range indices {
		ordered = append(ordered, index)
	}
	sort.Ints(ordered)

	out := make([]Provider, 0, len(ordered))
	for _, index := range ordered {
		prefix := fmt.Sprintf("PODDY_EXTERNAL_AUTH_%d_", index)
		provider := Provider{
			ID:           strings.TrimSpace(values[prefix+"ID"]),
			Type:         strings.ToLower(strings.TrimSpace(values[prefix+"TYPE"])),
			DisplayName:  strings.TrimSpace(values[prefix+"DISPLAY_NAME"]),
			DisplayIcon:  strings.TrimSpace(values[prefix+"DISPLAY_ICON"]),
			ClientID:     strings.TrimSpace(values[prefix+"CLIENT_ID"]),
			ClientSecret: values[prefix+"CLIENT_SECRET"],
			AuthURL:      strings.TrimSpace(values[prefix+"AUTH_URL"]),
			TokenURL:     strings.TrimSpace(values[prefix+"TOKEN_URL"]),
			ValidateURL:  strings.TrimSpace(values[prefix+"VALIDATE_URL"]),
			RevokeURL:    strings.TrimSpace(values[prefix+"REVOKE_URL"]),
			RedirectURL:  strings.TrimSpace(values[prefix+"REDIRECT_URL"]),
			Regex:        strings.TrimSpace(values[prefix+"REGEX"]),
			Username:     strings.TrimSpace(values[prefix+"USERNAME"]),
			Scopes:       splitList(values[prefix+"SCOPES"]),
			Env:          splitList(values[prefix+"ENV"]),
			PKCE:         parseBool(values[prefix+"PKCE"], true),
		}
		applyDefaults(&provider)
		if err := validateProvider(&provider); err != nil {
			return nil, fmt.Errorf("external auth %d (%s): %w", index, provider.ID, err)
		}
		out = append(out, provider)
	}

	seen := map[string]bool{}
	for _, provider := range out {
		if seen[provider.ID] {
			return nil, fmt.Errorf("duplicate external auth provider id %q", provider.ID)
		}
		seen[provider.ID] = true
	}
	return out, nil
}

func applyDefaults(p *Provider) {
	if p.Type == "" {
		p.Type = "oidc"
	}
	if p.DisplayName == "" {
		p.DisplayName = title(p.Type)
	}
	if p.Username == "" {
		switch p.Type {
		case "github":
			p.Username = "x-access-token"
		default:
			p.Username = "oauth2"
		}
	}
	if len(p.Env) == 0 {
		switch p.Type {
		case "github":
			p.Env = []string{"GITHUB_TOKEN", "GH_TOKEN"}
		case "gitlab":
			p.Env = []string{"GITLAB_TOKEN"}
		case "gitea":
			p.Env = []string{"GITEA_TOKEN"}
		}
	}

	switch p.Type {
	case "github":
		setIfEmpty(&p.AuthURL, "https://github.com/login/oauth/authorize")
		setIfEmpty(&p.TokenURL, "https://github.com/login/oauth/access_token")
		setIfEmpty(&p.ValidateURL, "https://api.github.com/user")
		setIfEmpty(&p.Regex, `^(https?://)?(www\.)?github\.com(/.*)?$`)
		if len(p.Scopes) == 0 {
			p.Scopes = []string{"repo", "read:user", "user:email"}
		}
	case "gitlab":
		setIfEmpty(&p.AuthURL, "https://gitlab.com/oauth/authorize")
		setIfEmpty(&p.TokenURL, "https://gitlab.com/oauth/token")
		setIfEmpty(&p.ValidateURL, "https://gitlab.com/oauth/token/info")
		setIfEmpty(&p.RevokeURL, "https://gitlab.com/oauth/revoke")
		setIfEmpty(&p.Regex, `^(https?://)?gitlab\.com(/.*)?$`)
		if len(p.Scopes) == 0 {
			p.Scopes = []string{"write_repository", "read_api"}
		}
	case "gitea":
		setIfEmpty(&p.AuthURL, "https://gitea.com/login/oauth/authorize")
		setIfEmpty(&p.TokenURL, "https://gitea.com/login/oauth/access_token")
		setIfEmpty(&p.Regex, `^(https?://)?gitea\.com(/.*)?$`)
		if len(p.Scopes) == 0 {
			p.Scopes = []string{"read:user", "write:repository"}
		}
	case "azure-devops", "azure-devops-entra":
		setIfEmpty(&p.AuthURL, "https://app.vssps.visualstudio.com/oauth2/authorize")
		setIfEmpty(&p.TokenURL, "https://app.vssps.visualstudio.com/oauth2/token")
		setIfEmpty(&p.Regex, `^(https?://)?dev\.azure\.com(/.*)?$`)
	case "bitbucket-cloud":
		setIfEmpty(&p.AuthURL, "https://bitbucket.org/site/oauth2/authorize")
		setIfEmpty(&p.TokenURL, "https://bitbucket.org/site/oauth2/access_token")
		setIfEmpty(&p.Regex, `^(https?://)?bitbucket\.org(/.*)?$`)
	}
}

func validateProvider(p *Provider) error {
	if p.ID == "" {
		return fmt.Errorf("id is required")
	}
	if !regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,62}$`).MatchString(p.ID) {
		return fmt.Errorf("id must use letters, numbers, dots, dashes or underscores")
	}
	if p.ClientID == "" {
		return fmt.Errorf("client id is required")
	}
	if p.AuthURL == "" || p.TokenURL == "" {
		return fmt.Errorf("auth url and token url are required")
	}
	for name, raw := range map[string]string{"auth url": p.AuthURL, "token url": p.TokenURL} {
		u, err := url.Parse(raw)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return fmt.Errorf("%s must be an absolute URL", name)
		}
	}
	if p.Regex != "" {
		re, err := regexp.Compile(p.Regex)
		if err != nil {
			return fmt.Errorf("invalid regex: %w", err)
		}
		p.compiledRegex = re
	}
	for _, env := range p.Env {
		if !regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`).MatchString(env) {
			return fmt.Errorf("invalid env name %q", env)
		}
	}
	return nil
}

// MatchHost reports whether a Git host or URL belongs to this provider.
func (p *Provider) MatchHost(value string) bool {
	if p == nil || value == "" {
		return false
	}
	if p.compiledRegex == nil && p.Regex != "" {
		re, err := regexp.Compile(p.Regex)
		if err == nil {
			p.compiledRegex = re
		}
	}
	if p.compiledRegex != nil {
		return p.compiledRegex.MatchString(value)
	}
	return false
}

func setIfEmpty(target *string, value string) {
	if *target == "" {
		*target = value
	}
}

func splitList(value string) []string {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\n'
	})
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		if field = strings.TrimSpace(field); field != "" {
			out = append(out, field)
		}
	}
	return out
}

func parseBool(value string, fallback bool) bool {
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func title(value string) string {
	if value == "" {
		return ""
	}
	return strings.ToUpper(value[:1]) + value[1:]
}
