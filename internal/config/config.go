package config

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"poddy/internal/extension"
)

type Config struct {
	Addr                      string
	MetricsAddr               string
	Namespace                 string
	RBACConfigMap             string
	StateConfigMap            string
	TemplatesDir              string
	DefaultTemplate           string
	StorageClass              string
	ImageRegistry             string
	ImageRegistryMapRaw       string
	ImageRegistryMap          map[string]string
	WorkspaceImagePullSecrets string

	ExternalAuthSecret       string
	ExternalAuthKey          string
	ExternalAuthURL          string
	ExternalAuthRedirectBase string
	Token                    string
	TokenAdmin               bool
	Kubeconfig               string
	Context                  string
	K8sQPS                   float64
	K8sBurst                 int
	Editor                   string
	DesktopSecret            string

	RegionMode   bool
	RegionName   string
	RegionKey    string
	RegionHubURL string
	RegionsFile  string
	InstanceID   string

	BackupS3Endpoint  string
	BackupS3Region    string
	BackupS3Bucket    string
	BackupS3AccessKey string
	BackupS3SecretKey string
	BackupS3Prefix    string
	BackupS3UseSSL    bool
	BackupHelperImage string

	DatabaseURL            string
	DatabaseAutoMigrate    bool
	DatabaseConnectTimeout time.Duration

	SSHEnabled           bool
	SSHAddr              string
	SSHHostKey           string
	SSHKeys              string
	SSHKeysFile          string
	SSHPublicHost        string
	SSHPublicPort        string
	SSHAllowUnmappedKeys bool
	SSHUserKeysConfigMap string
	SSHRequireAgent      bool
	SSHCertTTL           time.Duration
	SSHCAKeyFile         string
	AgentAddr            string
	AgentURL             string
	AgentImage           string
	AgentKey             string
	AgentTokenTTL        time.Duration

	VSCodeExtensionID          string
	VSCodeExtensionVersion     string
	VSCodeExtensionReleaseURL  string
	VSCodeExtensionDownloadURL string

	OIDCIssuer           string
	OIDCDiscoveryURL     string
	OIDCAuthorizationURL string
	OIDCClientID         string
	OIDCClientSecret     string
	OIDCRedirectURL      string
	OIDCScopes           string
	OIDCAllowedGroups    string
	OIDCAllowedUsers     string
	OIDCUsernameClaim    string
	OIDCEmailClaim       string
	OIDCGroupsClaim      string
	OIDCSessionKey       string
	OIDCSessionTTL       time.Duration
}

func Load(args []string) (Config, error) {
	ext := extension.Default()
	cfg := Config{
		Addr:                      env("PODDY_ADDR", ":8090"),
		MetricsAddr:               os.Getenv("PODDY_METRICS_ADDR"),
		Namespace:                 env("PODDY_NAMESPACE", "default"),
		RBACConfigMap:             env("PODDY_RBAC_CONFIGMAP", "poddy-rbac"),
		StateConfigMap:            env("PODDY_STATE_CONFIGMAP", "poddy-state"),
		TemplatesDir:              os.Getenv("PODDY_TEMPLATES_DIR"),
		DefaultTemplate:           env("PODDY_TEMPLATE", "base"),
		StorageClass:              os.Getenv("PODDY_STORAGE_CLASS"),
		ImageRegistry:             os.Getenv("PODDY_IMAGE_REGISTRY"),
		ImageRegistryMapRaw:       os.Getenv("PODDY_IMAGE_REGISTRY_MAP"),
		WorkspaceImagePullSecrets: os.Getenv("PODDY_WORKSPACE_IMAGE_PULL_SECRETS"),

		ExternalAuthSecret:       env("PODDY_EXTERNAL_AUTH_SECRET", "poddy-external-auth"),
		ExternalAuthKey:          os.Getenv("PODDY_EXTERNAL_AUTH_KEY"),
		ExternalAuthURL:          os.Getenv("PODDY_EXTERNAL_AUTH_URL"),
		ExternalAuthRedirectBase: os.Getenv("PODDY_EXTERNAL_AUTH_REDIRECT_BASE"),
		Token:                    os.Getenv("PODDY_TOKEN"),
		TokenAdmin:               envBool("PODDY_TOKEN_ADMIN", false),
		Kubeconfig:               os.Getenv("KUBECONFIG"),
		K8sQPS:                   envFloat("PODDY_K8S_QPS", 50),
		K8sBurst:                 envInt("PODDY_K8S_BURST", 100),
		Editor:                   env("PODDY_EDITOR", "code"),
		DesktopSecret:            os.Getenv("PODDY_DESKTOP_SECRET"),

		RegionMode:   envBool("PODDY_REGION_MODE", false),
		RegionName:   strings.TrimSpace(os.Getenv("PODDY_REGION_NAME")),
		RegionKey:    os.Getenv("PODDY_REGION_KEY"),
		RegionHubURL: strings.TrimRight(os.Getenv("PODDY_REGION_HUB_URL"), "/"),
		RegionsFile:  os.Getenv("PODDY_REGIONS_FILE"),
		InstanceID:   os.Getenv("PODDY_INSTANCE_ID"),

		BackupS3Endpoint:  os.Getenv("PODDY_BACKUP_S3_ENDPOINT"),
		BackupS3Region:    os.Getenv("PODDY_BACKUP_S3_REGION"),
		BackupS3Bucket:    os.Getenv("PODDY_BACKUP_S3_BUCKET"),
		BackupS3AccessKey: os.Getenv("PODDY_BACKUP_S3_ACCESS_KEY"),
		BackupS3SecretKey: os.Getenv("PODDY_BACKUP_S3_SECRET_KEY"),
		BackupS3Prefix:    env("PODDY_BACKUP_S3_PREFIX", "poddy-backups"),
		BackupS3UseSSL:    envBool("PODDY_BACKUP_S3_USE_SSL", true),
		BackupHelperImage: env("PODDY_BACKUP_HELPER_IMAGE", "busybox:1.36"),

		DatabaseURL:            os.Getenv("PODDY_DATABASE_URL"),
		DatabaseAutoMigrate:    envBool("PODDY_DATABASE_AUTO_MIGRATE", true),
		DatabaseConnectTimeout: envDuration("PODDY_DATABASE_CONNECT_TIMEOUT", 30*time.Second),

		SSHEnabled:           envBool("PODDY_SSH_ENABLED", true),
		SSHAddr:              env("PODDY_SSH_ADDR", "127.0.0.1:2222"),
		SSHHostKey:           os.Getenv("PODDY_SSH_HOST_KEY"),
		SSHKeys:              os.Getenv("PODDY_SSH_KEYS"),
		SSHKeysFile:          os.Getenv("PODDY_SSH_KEYS_FILE"),
		SSHPublicHost:        env("PODDY_SSH_PUBLIC_HOST", "127.0.0.1"),
		SSHPublicPort:        env("PODDY_SSH_PUBLIC_PORT", "2222"),
		SSHAllowUnmappedKeys: envBool("PODDY_SSH_ALLOW_UNMAPPED_KEYS", false),
		SSHUserKeysConfigMap: env("PODDY_SSH_USER_KEYS_CONFIGMAP", "poddy-user-ssh-keys"),
		SSHRequireAgent:      envBool("PODDY_SSH_REQUIRE_AGENT", false),
		SSHCertTTL:           envDuration("PODDY_SSH_CERT_TTL", 15*time.Minute),
		SSHCAKeyFile:         os.Getenv("PODDY_SSH_CA_KEY_FILE"),
		AgentAddr:            os.Getenv("PODDY_AGENT_ADDR"),
		AgentURL:             os.Getenv("PODDY_AGENT_URL"),
		AgentImage:           os.Getenv("PODDY_AGENT_IMAGE"),
		AgentKey:             os.Getenv("PODDY_AGENT_KEY"),
		AgentTokenTTL:        envDuration("PODDY_AGENT_TOKEN_TTL", 7*24*time.Hour),

		VSCodeExtensionID:          env("PODDY_VSCODE_EXTENSION_ID", ext.ID),
		VSCodeExtensionVersion:     env("PODDY_VSCODE_EXTENSION_VERSION", ext.Version),
		VSCodeExtensionReleaseURL:  env("PODDY_VSCODE_EXTENSION_RELEASE_URL", ext.ReleaseURL),
		VSCodeExtensionDownloadURL: env("PODDY_VSCODE_EXTENSION_DOWNLOAD_URL", ext.DownloadURL),

		OIDCIssuer:           os.Getenv("PODDY_OIDC_ISSUER"),
		OIDCDiscoveryURL:     os.Getenv("PODDY_OIDC_DISCOVERY_URL"),
		OIDCAuthorizationURL: os.Getenv("PODDY_OIDC_AUTHORIZATION_URL"),
		OIDCClientID:         os.Getenv("PODDY_OIDC_CLIENT_ID"),
		OIDCClientSecret:     os.Getenv("PODDY_OIDC_CLIENT_SECRET"),
		OIDCRedirectURL:      os.Getenv("PODDY_OIDC_REDIRECT_URL"),
		OIDCScopes:           env("PODDY_OIDC_SCOPES", "openid,profile,email,groups"),
		OIDCAllowedGroups:    os.Getenv("PODDY_OIDC_ALLOWED_GROUPS"),
		OIDCAllowedUsers:     os.Getenv("PODDY_OIDC_ALLOWED_USERS"),
		OIDCUsernameClaim:    env("PODDY_OIDC_USERNAME_CLAIM", "preferred_username"),
		OIDCEmailClaim:       env("PODDY_OIDC_EMAIL_CLAIM", "email"),
		OIDCGroupsClaim:      env("PODDY_OIDC_GROUPS_CLAIM", "groups"),
		OIDCSessionKey:       os.Getenv("PODDY_OIDC_SESSION_KEY"),
		OIDCSessionTTL:       envDuration("PODDY_OIDC_SESSION_TTL", 12*time.Hour),
	}

	fs := flag.NewFlagSet("poddy", flag.ContinueOnError)
	fs.StringVar(&cfg.Addr, "addr", cfg.Addr, "listen address")
	fs.StringVar(&cfg.MetricsAddr, "metrics-addr", cfg.MetricsAddr, "Prometheus metrics listen address (empty disables metrics)")
	fs.StringVar(&cfg.Namespace, "namespace", cfg.Namespace, "workspace namespace")
	fs.StringVar(&cfg.RBACConfigMap, "rbac-configmap", cfg.RBACConfigMap, "ConfigMap storing admins and template access")
	fs.StringVar(&cfg.StateConfigMap, "state-configmap", cfg.StateConfigMap, "legacy ConfigMap storing stopped workspace specs (migration only)")
	fs.StringVar(&cfg.TemplatesDir, "templates-dir", cfg.TemplatesDir, "extra workspace templates directory")
	fs.StringVar(&cfg.DefaultTemplate, "template", cfg.DefaultTemplate, "default workspace template")
	fs.StringVar(&cfg.StorageClass, "storage-class", cfg.StorageClass, "default StorageClass for workspace home volumes")
	fs.StringVar(&cfg.ImageRegistry, "image-registry", cfg.ImageRegistry, "registry mirror applied to workspace and sidecar images")
	fs.StringVar(&cfg.ImageRegistryMapRaw, "image-registry-map", cfg.ImageRegistryMapRaw, "per-host registry mirrors (JSON object or host=target pairs)")
	fs.StringVar(&cfg.WorkspaceImagePullSecrets, "workspace-image-pull-secrets", cfg.WorkspaceImagePullSecrets, "comma-separated imagePullSecrets applied to workspace pods")
	fs.StringVar(&cfg.ExternalAuthSecret, "external-auth-secret", cfg.ExternalAuthSecret, "Secret storing per-user external auth tokens")
	fs.StringVar(&cfg.ExternalAuthKey, "external-auth-key", cfg.ExternalAuthKey, "key used to encrypt external auth tokens and sign workspace tokens")
	fs.StringVar(&cfg.ExternalAuthURL, "external-auth-url", cfg.ExternalAuthURL, "portal URL reachable from workspaces (defaults to the request URL)")
	fs.StringVar(&cfg.ExternalAuthRedirectBase, "external-auth-redirect-base", cfg.ExternalAuthRedirectBase, "public base URL for external auth callbacks")
	fs.StringVar(&cfg.Token, "token", cfg.Token, "optional shared access token (single identity; not for multi-user SSH)")
	fs.BoolVar(&cfg.TokenAdmin, "token-admin", cfg.TokenAdmin, "treat the shared token as a global admin (unsafe for shared tokens)")
	fs.StringVar(&cfg.Kubeconfig, "kubeconfig", cfg.Kubeconfig, "path to kubeconfig")
	fs.StringVar(&cfg.Context, "context", cfg.Context, "kubeconfig context")
	fs.Float64Var(&cfg.K8sQPS, "k8s-qps", cfg.K8sQPS, "client-side Kubernetes API QPS limit (0 uses the client default)")
	fs.IntVar(&cfg.K8sBurst, "k8s-burst", cfg.K8sBurst, "client-side Kubernetes API burst limit")
	fs.StringVar(&cfg.Editor, "editor", cfg.Editor, "editor command used to open VS Code (empty disables local launch)")
	fs.StringVar(&cfg.DesktopSecret, "desktop-secret", cfg.DesktopSecret, "secret used to sign VS Code desktop tickets (set in HA)")
	fs.BoolVar(&cfg.RegionMode, "region-mode", cfg.RegionMode, "run as a region manager trusted by a central hub")
	fs.StringVar(&cfg.RegionName, "region-name", cfg.RegionName, "region identifier")
	fs.StringVar(&cfg.RegionKey, "region-key", cfg.RegionKey, "shared secret used to verify the central hub")
	fs.StringVar(&cfg.RegionHubURL, "region-hub-url", cfg.RegionHubURL, "central hub public URL")
	fs.StringVar(&cfg.RegionsFile, "regions-file", cfg.RegionsFile, "central hub regions configuration file")
	fs.StringVar(&cfg.InstanceID, "instance-id", cfg.InstanceID, "unique instance identifier used for distributed locks")
	fs.StringVar(&cfg.BackupS3Endpoint, "backup-s3-endpoint", cfg.BackupS3Endpoint, "S3-compatible endpoint for volume backups")
	fs.StringVar(&cfg.BackupS3Region, "backup-s3-region", cfg.BackupS3Region, "S3 region for volume backups")
	fs.StringVar(&cfg.BackupS3Bucket, "backup-s3-bucket", cfg.BackupS3Bucket, "S3 bucket for volume backups")
	fs.StringVar(&cfg.BackupS3AccessKey, "backup-s3-access-key", cfg.BackupS3AccessKey, "S3 access key for volume backups")
	fs.StringVar(&cfg.BackupS3SecretKey, "backup-s3-secret-key", cfg.BackupS3SecretKey, "S3 secret key for volume backups")
	fs.StringVar(&cfg.BackupS3Prefix, "backup-s3-prefix", cfg.BackupS3Prefix, "object key prefix for volume backups")
	fs.BoolVar(&cfg.BackupS3UseSSL, "backup-s3-use-ssl", cfg.BackupS3UseSSL, "use TLS for the S3 endpoint")
	fs.StringVar(&cfg.BackupHelperImage, "backup-helper-image", cfg.BackupHelperImage, "image used by temporary volume backup pods")
	fs.StringVar(&cfg.DatabaseURL, "database-url", cfg.DatabaseURL, "PostgreSQL URL for optional metadata mirror and audit events")
	fs.BoolVar(&cfg.DatabaseAutoMigrate, "database-auto-migrate", cfg.DatabaseAutoMigrate, "apply embedded PostgreSQL migrations on startup")
	fs.DurationVar(&cfg.DatabaseConnectTimeout, "database-connect-timeout", cfg.DatabaseConnectTimeout, "how long to retry PostgreSQL before disabling metadata and audit")
	fs.BoolVar(&cfg.SSHEnabled, "ssh-enabled", cfg.SSHEnabled, "show SSH/VS Code connection options in the UI")
	fs.StringVar(&cfg.SSHAddr, "ssh-addr", cfg.SSHAddr, "SSH bastion listen address, empty disables the local bastion")
	fs.StringVar(&cfg.SSHHostKey, "ssh-host-key", cfg.SSHHostKey, "SSH bastion host key path")
	fs.StringVar(&cfg.SSHKeys, "ssh-keys", cfg.SSHKeys, "comma-separated SSH public keys authorized for workspaces")
	fs.StringVar(&cfg.SSHKeysFile, "ssh-keys-file", cfg.SSHKeysFile, "authorized_keys file for workspaces")
	fs.StringVar(&cfg.SSHPublicHost, "ssh-public-host", cfg.SSHPublicHost, "public SSH host shown to users")
	fs.StringVar(&cfg.SSHPublicPort, "ssh-public-port", cfg.SSHPublicPort, "public SSH port shown to users")
	fs.BoolVar(&cfg.SSHAllowUnmappedKeys, "ssh-allow-unmapped-keys", cfg.SSHAllowUnmappedKeys, "allow SSH keys without an owner comment to access every workspace (legacy, insecure)")
	fs.StringVar(&cfg.SSHUserKeysConfigMap, "ssh-user-keys-configmap", cfg.SSHUserKeysConfigMap, "ConfigMap storing self-service SSH keys")
	fs.BoolVar(&cfg.SSHRequireAgent, "ssh-require-agent", cfg.SSHRequireAgent, "require an outbound workspace agent tunnel for SSH (disables Kubernetes port-forward fallback)")
	fs.DurationVar(&cfg.SSHCertTTL, "ssh-cert-ttl", cfg.SSHCertTTL, "lifetime of issued SSH certificates")
	fs.StringVar(&cfg.SSHCAKeyFile, "ssh-ca-key-file", cfg.SSHCAKeyFile, "path to the SSH certificate authority private key")
	fs.StringVar(&cfg.AgentAddr, "agent-addr", cfg.AgentAddr, "listen address for the workspace agent tunnel (empty disables it)")
	fs.StringVar(&cfg.AgentURL, "agent-url", cfg.AgentURL, "URL injected into workspaces for the agent tunnel")
	fs.StringVar(&cfg.AgentImage, "agent-image", cfg.AgentImage, "image running the poddy-agent sidecar (empty uses the in-workspace agent)")
	fs.StringVar(&cfg.AgentKey, "agent-key", cfg.AgentKey, "shared key used to sign and verify workspace agent tokens")
	fs.DurationVar(&cfg.AgentTokenTTL, "agent-token-ttl", cfg.AgentTokenTTL, "lifetime of workspace agent tokens")
	fs.StringVar(&cfg.VSCodeExtensionID, "vscode-extension-id", cfg.VSCodeExtensionID, "VS Code extension identifier shown in the portal")
	fs.StringVar(&cfg.VSCodeExtensionVersion, "vscode-extension-version", cfg.VSCodeExtensionVersion, "VS Code extension version shown in the portal")
	fs.StringVar(&cfg.VSCodeExtensionReleaseURL, "vscode-extension-release-url", cfg.VSCodeExtensionReleaseURL, "VS Code extension release page")
	fs.StringVar(&cfg.VSCodeExtensionDownloadURL, "vscode-extension-download-url", cfg.VSCodeExtensionDownloadURL, "VS Code extension VSIX download URL")

	fs.StringVar(&cfg.OIDCIssuer, "oidc-issuer", cfg.OIDCIssuer, "OIDC issuer URL (enables OIDC login)")
	fs.StringVar(&cfg.OIDCDiscoveryURL, "oidc-discovery-url", cfg.OIDCDiscoveryURL, "OIDC discovery URL (defaults to the issuer)")
	fs.StringVar(&cfg.OIDCAuthorizationURL, "oidc-authorization-url", cfg.OIDCAuthorizationURL, "browser-facing OIDC authorization endpoint override")
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
	imageRegistryMap, err := parseImageRegistryMap(cfg.ImageRegistryMapRaw)
	if err != nil {
		return cfg, fmt.Errorf("image-registry-map: %w", err)
	}
	cfg.ImageRegistryMap = imageRegistryMap
	cfg.DatabaseURL = strings.TrimSpace(cfg.DatabaseURL)
	if cfg.RegionMode {
		cfg.RegionName = strings.TrimSpace(cfg.RegionName)
		if cfg.RegionName == "" {
			cfg.RegionName = "region"
		}
		if cfg.RegionKey == "" {
			return cfg, fmt.Errorf("region-key is required in region mode")
		}
	}
	return cfg, nil
}

func parseImageRegistryMap(raw string) (map[string]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	if strings.HasPrefix(raw, "{") {
		var mappings map[string]string
		if err := json.Unmarshal([]byte(raw), &mappings); err != nil {
			return nil, err
		}
		if len(mappings) == 0 {
			return nil, nil
		}
		return mappings, nil
	}
	mappings := map[string]string{}
	for _, pair := range strings.Split(raw, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		key, value, ok := strings.Cut(pair, "=")
		if !ok {
			return nil, fmt.Errorf("expected host=target, got %q", pair)
		}
		key = strings.TrimSpace(key)
		if key == "" {
			return nil, fmt.Errorf("empty registry host")
		}
		mappings[key] = strings.TrimSpace(value)
	}
	if len(mappings) == 0 {
		return nil, nil
	}
	return mappings, nil
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

func envInt(key string, fallback int) int {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil {
			return parsed
		}
	}
	return fallback
}

func envFloat(key string, fallback float64) float64 {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if parsed, err := strconv.ParseFloat(v, 64); err == nil {
			return parsed
		}
	}
	return fallback
}
