package config

import (
	"flag"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Addr            string
	Namespace       string
	RBACConfigMap   string
	StateConfigMap  string
	TemplatesDir    string
	DefaultTemplate string

	ExternalAuthSecret       string
	ExternalAuthKey          string
	ExternalAuthURL          string
	ExternalAuthRedirectBase string
	Token                    string
	Kubeconfig               string
	Context                  string
	Editor                   string

	SSHEnabled           bool
	SSHAddr              string
	SSHHostKey           string
	SSHKeys              string
	SSHKeysFile          string
	SSHPublicHost        string
	SSHPublicPort        string
	SSHAllowUnmappedKeys bool

	OIDCIssuer        string
	OIDCClientID      string
	OIDCClientSecret  string
	OIDCRedirectURL   string
	OIDCScopes        string
	OIDCAllowedGroups string
	OIDCAllowedUsers  string
	OIDCUsernameClaim string
	OIDCEmailClaim    string
	OIDCGroupsClaim   string
	OIDCSessionKey    string
	OIDCSessionTTL    time.Duration
}

func Load(args []string) (Config, error) {
	cfg := Config{
		Addr:            env("PODDY_ADDR", ":8090"),
		Namespace:       env("PODDY_NAMESPACE", "default"),
		RBACConfigMap:   env("PODDY_RBAC_CONFIGMAP", "poddy-rbac"),
		StateConfigMap:  env("PODDY_STATE_CONFIGMAP", "poddy-state"),
		TemplatesDir:    os.Getenv("PODDY_TEMPLATES_DIR"),
		DefaultTemplate: env("PODDY_TEMPLATE", "base"),

		ExternalAuthSecret:       env("PODDY_EXTERNAL_AUTH_SECRET", "poddy-external-auth"),
		ExternalAuthKey:          os.Getenv("PODDY_EXTERNAL_AUTH_KEY"),
		ExternalAuthURL:          os.Getenv("PODDY_EXTERNAL_AUTH_URL"),
		ExternalAuthRedirectBase: os.Getenv("PODDY_EXTERNAL_AUTH_REDIRECT_BASE"),
		Token:                    os.Getenv("PODDY_TOKEN"),
		Kubeconfig:               os.Getenv("KUBECONFIG"),
		Editor:                   env("PODDY_EDITOR", "code"),

		SSHEnabled:           envBool("PODDY_SSH_ENABLED", true),
		SSHAddr:              env("PODDY_SSH_ADDR", "127.0.0.1:2222"),
		SSHHostKey:           os.Getenv("PODDY_SSH_HOST_KEY"),
		SSHKeys:              os.Getenv("PODDY_SSH_KEYS"),
		SSHKeysFile:          os.Getenv("PODDY_SSH_KEYS_FILE"),
		SSHPublicHost:        env("PODDY_SSH_PUBLIC_HOST", "127.0.0.1"),
		SSHPublicPort:        env("PODDY_SSH_PUBLIC_PORT", "2222"),
		SSHAllowUnmappedKeys: envBool("PODDY_SSH_ALLOW_UNMAPPED_KEYS", false),

		OIDCIssuer:        os.Getenv("PODDY_OIDC_ISSUER"),
		OIDCClientID:      os.Getenv("PODDY_OIDC_CLIENT_ID"),
		OIDCClientSecret:  os.Getenv("PODDY_OIDC_CLIENT_SECRET"),
		OIDCRedirectURL:   os.Getenv("PODDY_OIDC_REDIRECT_URL"),
		OIDCScopes:        env("PODDY_OIDC_SCOPES", "openid,profile,email,groups"),
		OIDCAllowedGroups: os.Getenv("PODDY_OIDC_ALLOWED_GROUPS"),
		OIDCAllowedUsers:  os.Getenv("PODDY_OIDC_ALLOWED_USERS"),
		OIDCUsernameClaim: env("PODDY_OIDC_USERNAME_CLAIM", "preferred_username"),
		OIDCEmailClaim:    env("PODDY_OIDC_EMAIL_CLAIM", "email"),
		OIDCGroupsClaim:   env("PODDY_OIDC_GROUPS_CLAIM", "groups"),
		OIDCSessionKey:    os.Getenv("PODDY_OIDC_SESSION_KEY"),
		OIDCSessionTTL:    envDuration("PODDY_OIDC_SESSION_TTL", 12*time.Hour),
	}

	fs := flag.NewFlagSet("poddy", flag.ContinueOnError)
	fs.StringVar(&cfg.Addr, "addr", cfg.Addr, "listen address")
	fs.StringVar(&cfg.Namespace, "namespace", cfg.Namespace, "workspace namespace")
	fs.StringVar(&cfg.RBACConfigMap, "rbac-configmap", cfg.RBACConfigMap, "ConfigMap storing admins and template access")
	fs.StringVar(&cfg.StateConfigMap, "state-configmap", cfg.StateConfigMap, "ConfigMap storing stopped workspace specs")
	fs.StringVar(&cfg.TemplatesDir, "templates-dir", cfg.TemplatesDir, "extra workspace templates directory")
	fs.StringVar(&cfg.DefaultTemplate, "template", cfg.DefaultTemplate, "default workspace template")
	fs.StringVar(&cfg.ExternalAuthSecret, "external-auth-secret", cfg.ExternalAuthSecret, "Secret storing per-user external auth tokens")
	fs.StringVar(&cfg.ExternalAuthKey, "external-auth-key", cfg.ExternalAuthKey, "key used to encrypt external auth tokens and sign workspace tokens")
	fs.StringVar(&cfg.ExternalAuthURL, "external-auth-url", cfg.ExternalAuthURL, "portal URL reachable from workspaces (defaults to the request URL)")
	fs.StringVar(&cfg.ExternalAuthRedirectBase, "external-auth-redirect-base", cfg.ExternalAuthRedirectBase, "public base URL for external auth callbacks")
	fs.StringVar(&cfg.Token, "token", cfg.Token, "optional shared access token")
	fs.StringVar(&cfg.Kubeconfig, "kubeconfig", cfg.Kubeconfig, "path to kubeconfig")
	fs.StringVar(&cfg.Context, "context", cfg.Context, "kubeconfig context")
	fs.StringVar(&cfg.Editor, "editor", cfg.Editor, "editor command used to open VS Code (empty disables local launch)")
	fs.BoolVar(&cfg.SSHEnabled, "ssh-enabled", cfg.SSHEnabled, "show SSH/VS Code connection options in the UI")
	fs.StringVar(&cfg.SSHAddr, "ssh-addr", cfg.SSHAddr, "SSH bastion listen address, empty disables the local bastion")
	fs.StringVar(&cfg.SSHHostKey, "ssh-host-key", cfg.SSHHostKey, "SSH bastion host key path")
	fs.StringVar(&cfg.SSHKeys, "ssh-keys", cfg.SSHKeys, "comma-separated SSH public keys authorized for workspaces")
	fs.StringVar(&cfg.SSHKeysFile, "ssh-keys-file", cfg.SSHKeysFile, "authorized_keys file for workspaces")
	fs.StringVar(&cfg.SSHPublicHost, "ssh-public-host", cfg.SSHPublicHost, "public SSH host shown to users")
	fs.StringVar(&cfg.SSHPublicPort, "ssh-public-port", cfg.SSHPublicPort, "public SSH port shown to users")
	fs.BoolVar(&cfg.SSHAllowUnmappedKeys, "ssh-allow-unmapped-keys", cfg.SSHAllowUnmappedKeys, "allow SSH keys without an owner comment to access every workspace (legacy, insecure)")

	fs.StringVar(&cfg.OIDCIssuer, "oidc-issuer", cfg.OIDCIssuer, "OIDC issuer URL (enables OIDC login)")
	fs.StringVar(&cfg.OIDCClientID, "oidc-client-id", cfg.OIDCClientID, "OIDC client ID")
	fs.StringVar(&cfg.OIDCClientSecret, "oidc-client-secret", cfg.OIDCClientSecret, "OIDC client secret (empty for public clients with PKCE)")
	fs.StringVar(&cfg.OIDCRedirectURL, "oidc-redirect-url", cfg.OIDCRedirectURL, "OIDC callback URL (derived from the request when empty)")
	fs.StringVar(&cfg.OIDCScopes, "oidc-scopes", cfg.OIDCScopes, "comma-separated OIDC scopes")
	fs.StringVar(&cfg.OIDCAllowedGroups, "oidc-allowed-groups", cfg.OIDCAllowedGroups, "comma-separated allowed groups (empty allows all)")
	fs.StringVar(&cfg.OIDCAllowedUsers, "oidc-allowed-users", cfg.OIDCAllowedUsers, "comma-separated allowed usernames/emails (empty allows all)")
	fs.StringVar(&cfg.OIDCUsernameClaim, "oidc-username-claim", cfg.OIDCUsernameClaim, "OIDC claim used as username")
	fs.StringVar(&cfg.OIDCEmailClaim, "oidc-email-claim", cfg.OIDCEmailClaim, "OIDC claim used as email")
	fs.StringVar(&cfg.OIDCGroupsClaim, "oidc-groups-claim", cfg.OIDCGroupsClaim, "OIDC claim used as groups")
	fs.StringVar(&cfg.OIDCSessionKey, "oidc-session-key", cfg.OIDCSessionKey, "secret used to sign OIDC sessions (random when empty; set in HA)")
	fs.DurationVar(&cfg.OIDCSessionTTL, "oidc-session-ttl", cfg.OIDCSessionTTL, "OIDC session lifetime")

	if err := fs.Parse(args); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return parsed
}

func envDuration(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}
