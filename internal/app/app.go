// Package app wires the portal dependencies and owns the process lifecycle.
package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"k8s.io/client-go/kubernetes"

	"poddy/internal/access"
	accessremote "poddy/internal/access/remote"
	"poddy/internal/auth"
	"poddy/internal/backup"
	"poddy/internal/bastion"
	"poddy/internal/config"
	"poddy/internal/extension"
	"poddy/internal/externalauth"
	"poddy/internal/k8s"
	"poddy/internal/local"
	"poddy/internal/metadata"
	"poddy/internal/metrics"
	"poddy/internal/quota"
	quotaremote "poddy/internal/quota/remote"
	"poddy/internal/rbac"
	"poddy/internal/region"
	"poddy/internal/server"
	"poddy/internal/sshca"
	"poddy/internal/sshkeys"
	"poddy/internal/templates"
	"poddy/internal/tunnel"
	"poddy/internal/userkeys"
	"poddy/internal/userstore"
	"poddy/web"
)

// Run starts the portal and blocks until the process receives a termination
// signal or the HTTP server fails.
func Run(ctx context.Context, args []string) error {
	cfg, err := config.Load(args)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	if cfg.SSHRequireAgent && cfg.AgentURL == "" && cfg.AgentAddr == "" {
		return fmt.Errorf("workspace agent: -agent-url or -agent-addr is required when -ssh-require-agent=true")
	}

	sshCA, err := sshca.LoadOrGenerate(cfg.SSHCAKeyFile)
	if err != nil {
		return fmt.Errorf("ssh ca: %w", err)
	}
	if cfg.SSHCAKeyFile == "" {
		log.Printf("ssh ca: using an ephemeral key; set PODDY_SSH_CA_KEY_FILE to keep certificates valid across restarts")
	}

	var appMetrics *metrics.Metrics
	if cfg.MetricsAddr != "" {
		appMetrics = metrics.New()
	}

	metaStore, err := metadata.OpenWithRetry(ctx, cfg.DatabaseURL, cfg.DatabaseAutoMigrate, cfg.DatabaseConnectTimeout)
	if err != nil {
		log.Printf("metadata: PostgreSQL unavailable, continuing without metadata and audit: %v", err)
		metaStore = metadata.Nop{}
	}
	defer metaStore.Close()
	metadataEnabled := metaStore.AuditEnabled()
	if metadataEnabled {
		log.Printf("metadata: PostgreSQL mirror and audit enabled")
	}
	regionIdentity := region.Identity{User: auth.User{Username: "region", Subject: cfg.RegionName}, Admin: true}
	var hubClient *region.Client
	var accessStore access.Store
	var quotaStore quota.Store
	if cfg.RegionMode && cfg.RegionHubURL != "" {
		hubClient = region.NewClient(region.Config{Name: cfg.RegionName, URL: cfg.RegionHubURL, Key: cfg.RegionKey})
		accessStore = accessremote.New(hubClient, regionIdentity)
		quotaStore = quotaremote.New(hubClient, regionIdentity)
		log.Printf("workspace access and quotas: using hub control plane at %s", cfg.RegionHubURL)
	} else {
		accessStore = openAccessStore(metaStore, cfg.Namespace)
		quotaStore = openQuotaStore(metaStore, cfg.Namespace)
	}

	client, restConfig, err := k8s.New(cfg.Kubeconfig, cfg.Context, cfg.K8sQPS, cfg.K8sBurst)
	if err != nil {
		return fmt.Errorf("kubernetes: %w", err)
	}

	catalog, err := templates.Load(cfg.TemplatesDir)
	if err != nil {
		return fmt.Errorf("templates: %w", err)
	}
	catalog.SetImageRegistry(cfg.ImageRegistry)
	catalog.SetImageRegistryMap(cfg.ImageRegistryMap)

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	rbacK8s := rbac.NewStore(client, cfg.Namespace, cfg.RBACConfigMap)
	userK8s := userstore.NewStore(client, cfg.Namespace, userstore.DefaultConfigMap)
	shareK8s := k8s.NewShareStore(client, cfg.Namespace, "")
	var rbacStore rbac.ConfigStore = rbacK8s
	var userStore userstore.Directory = userK8s
	var shareStore server.ShareStore = shareK8s
	if pg, ok := metaStore.(*metadata.Postgres); ok {
		if !pg.PGFirstReady(ctx) {
			log.Printf("metadata: PostgreSQL-first tables are missing; keeping RBAC, users, and shares in Kubernetes (apply migrations or enable database.autoMigrate)")
		} else {
			rbacStore = pg.RBACStore(cfg.Namespace, rbacK8s)
			userStore = pg.UserStore(cfg.Namespace, userK8s)
			if secret := postgresShareSecret(cfg); secret != "" {
				shareStore = pg.ShareStore(cfg.Namespace, secret, shareK8s)
			} else {
				log.Printf("shares: PostgreSQL enabled but no stable encryption key is configured; keeping share links in Kubernetes (set PODDY_EXTERNAL_AUTH_KEY, PODDY_OIDC_SESSION_KEY or PODDY_DESKTOP_SECRET)")
			}
		}
	}
	if appMetrics != nil || metadataEnabled {
		catalog.SetOnReload(func() {
			if appMetrics != nil {
				appMetrics.TemplateReload("success")
			}
			if !metadataEnabled {
				return
			}
			syncCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			if err := syncTemplates(syncCtx, cfg.Namespace, metaStore, rbacStore, catalog); err != nil {
				log.Printf("metadata: template sync: %v", err)
			}
		})
	}

	if cfg.TemplatesDir != "" {
		log.Printf("watching templates in %s", cfg.TemplatesDir)
		go templates.Watch(ctx, catalog, cfg.TemplatesDir, 2*time.Second)
	}
	log.Printf("watching template ConfigMaps in namespace %s", cfg.Namespace)
	go k8s.WatchTemplates(ctx, client, cfg.Namespace, catalog)

	allowUnmapped := cfg.SSHAllowUnmappedKeys || (cfg.Token == "" && cfg.OIDCIssuer == "")
	keys, keyring, editor, err := loadSSHKeys(cfg, allowUnmapped)
	if err != nil {
		return err
	}

	userKeyStore := userkeys.NewStore(client, cfg.Namespace, cfg.SSHUserKeysConfigMap)
	if stored, err := userKeyStore.List(ctx); err != nil {
		log.Printf("ssh user keys: initial load: %v", err)
	} else if err := keyring.Update(keys, userkeys.Owned(stored), allowUnmapped); err != nil {
		return fmt.Errorf("ssh user keys: %w", err)
	}
	go userkeys.Watch(ctx, userKeyStore, keyring, keys, allowUnmapped)

	externalAuth, err := newExternalAuth(ctx, cfg, client, metaStore)
	if err != nil {
		return err
	}

	portalURL := cfg.ExternalAuthURL
	if cfg.RegionMode && cfg.RegionHubURL != "" {
		portalURL = cfg.RegionHubURL
	}
	manager := &k8s.Manager{
		Client:              client,
		Rest:                restConfig,
		Namespace:           cfg.Namespace,
		Templates:           catalog,
		DefaultTemplate:     cfg.DefaultTemplate,
		DefaultStorageClass: cfg.StorageClass,
		ImageRegistry:       cfg.ImageRegistry,
		ImageRegistryMap:    cfg.ImageRegistryMap,
		ImagePullSecrets:    splitList(cfg.WorkspaceImagePullSecrets),
		AuthorizedKeys:      keys,
		SSHKeys:             keyring,
		SSHCA:               sshCA,
		SSHRequireAgent:     cfg.SSHRequireAgent,
		StateConfigMap:      cfg.StateConfigMap,
		PortalURL:           portalURL,
		InstanceID:          cfg.InstanceID,
		Region:              cfg.RegionName,
		HelperImage:         templates.RewriteImageRegistryWithMap(cfg.BackupHelperImage, cfg.ImageRegistry, cfg.ImageRegistryMap),
		AgentImage:          cfg.AgentImage,
	}
	if externalAuth != nil {
		manager.ExternalAuth = func(ctx context.Context, owner, _ string, _ string, requirements []templates.ExternalAuth) (map[string]string, error) {
			return externalAuth.Prepare(ctx, strings.ToLower(strings.TrimSpace(owner)), requirements)
		}
		manager.ExternalAuthToken = externalAuth.SignWorkspace
	}
	if hubClient != nil {
		var tokens sync.Map
		manager.ExternalAuth = func(ctx context.Context, owner, workspace, portalURL string, requirements []templates.ExternalAuth) (map[string]string, error) {
			response, err := hubClient.FetchExternalAuth(ctx, region.ExternalAuthRequest{
				Owner:        owner,
				Workspace:    workspace,
				PortalURL:    portalURL,
				Requirements: requirements,
			}, regionIdentity)
			if err != nil {
				return nil, err
			}
			if response.Token != "" {
				tokens.Store(workspace, response.Token)
			}
			return response.Env, nil
		}
		manager.ExternalAuthToken = func(workspace, _ string) (string, error) {
			if token, ok := tokens.LoadAndDelete(workspace); ok {
				return token.(string), nil
			}
			return "", nil
		}
	}

	agentKey := agentSecret(cfg)
	if agentURL := cfg.AgentURL; agentURL != "" && agentKey == "" {
		log.Printf("workspace agent tunnel: using an ephemeral signing key; set PODDY_AGENT_KEY to share it with the bastion")
	}
	agentSigner := tunnel.NewTokenSigner(agentKey)
	agentURL := cfg.AgentURL
	if agentURL == "" && cfg.AgentAddr != "" {
		agentURL = localAgentURL(cfg.AgentAddr)
	}
	manager.AgentURL = agentURL
	manager.AgentToken = func(workspace, owner string) (string, error) {
		token, _, err := agentSigner.SignAgent(workspace, owner, cfg.AgentTokenTTL)
		return token, err
	}
	if agentURL == "" {
		log.Printf("workspace agent tunnel disabled; SSH falls back to Kubernetes port-forward")
	}

	if err := manager.MigrateLegacy(ctx); err != nil {
		log.Printf("legacy workspace migration: %v", err)
	}
	manager.StartInformers(ctx)

	go manager.RunScheduleLoop(ctx, k8s.DefaultScheduleInterval)
	log.Printf("workspace schedules enabled (every %s)", k8s.DefaultScheduleInterval)

	var metadataStore metadata.Store
	var metadataAuditor metadata.Auditor
	if metadataEnabled {
		metadataStore = metaStore
		metadataAuditor = metaStore
		if err := syncAll(ctx, cfg.Namespace, metaStore, rbacStore, userStore, manager, catalog); err != nil {
			return fmt.Errorf("metadata sync: %w", err)
		}
	}
	if err := migrateQuotas(ctx, quotaStore, rbacStore, metadataEnabled); err != nil {
		return fmt.Errorf("quota migration: %w", err)
	}

	if appMetrics != nil {
		appMetrics.SetWorkspaceLister(func(ctx context.Context) ([]metrics.WorkspaceInfo, error) {
			list, err := manager.List(ctx)
			if err != nil {
				return nil, err
			}
			out := make([]metrics.WorkspaceInfo, 0, len(list))
			for _, workspace := range list {
				out = append(out, metrics.WorkspaceInfo{Phase: workspace.Phase, Template: workspace.Template})
			}
			return out, nil
		})
		metricsServer, err := startMetrics(ctx, cfg.MetricsAddr, appMetrics.Handler())
		if err != nil {
			return err
		}
		defer metricsServer.Close()
	}

	sshBastion, err := startBastion(ctx, cfg, manager, keyring, metadataAuditor, agentSigner, sshCA, accessStore)
	if err != nil {
		return err
	}
	if sshBastion != nil {
		defer sshBastion.Close()
	}

	forwards := server.NewForwarder()
	defer forwards.Close()

	oidcAuth, err := newOIDC(ctx, cfg)
	if err != nil {
		return err
	}

	sshKeyPath := "~/.ssh/id_ed25519"
	windows := false
	windowsKeyPath := ""
	if editor != nil {
		sshKeyPath = editor.PreferredKey()
		windows = editor.Windows()
		windowsKeyPath = editor.WindowsKeyPath
	}

	desktopSecret := []byte(cfg.DesktopSecret)
	if len(desktopSecret) == 0 {
		desktopSecret = []byte(cfg.OIDCSessionKey)
	}
	if len(desktopSecret) == 0 {
		desktopSecret = []byte(cfg.ExternalAuthKey)
	}
	if len(desktopSecret) == 0 {
		desktopSecret = []byte(randomSecret())
		if cfg.Token == "" && cfg.OIDCIssuer == "" {
			log.Printf("desktop tickets: using an ephemeral key; set PODDY_DESKTOP_SECRET for multiple replicas")
		}
	}

	registry, err := region.Load(cfg.RegionsFile)
	if err != nil {
		return fmt.Errorf("regions: %w", err)
	}
	var hub *region.Hub
	var regionTunnel *tunnel.RegionBroker
	if len(registry.All()) > 0 {
		if cfg.RegionMode {
			return fmt.Errorf("regions: region mode cannot also act as a central hub")
		}
		regionTunnel = tunnel.NewRegionBroker(func(name, token string) error {
			cfg, ok := registry.Get(name)
			if !ok {
				return fmt.Errorf("unknown region %q", name)
			}
			claims, err := region.VerifyIdentity(cfg.Key, token)
			if err != nil {
				return err
			}
			if claims.Region != name || !claims.Admin {
				return fmt.Errorf("invalid region identity")
			}
			return nil
		})
		hub = region.NewHub(registry)
		hub.SetTunnel(regionTunnel)
		keySync := make(chan struct{}, 1)
		regionTunnel.SetOnConnect(func(string) {
			select {
			case keySync <- struct{}{}:
			default:
			}
		})
		log.Printf("central hub configured with %d regions", len(registry.All()))
		go syncRegionKeys(ctx, hub, userKeyStore, 2*time.Minute, keySync)
	}

	if cfg.RegionMode && cfg.RegionHubURL != "" {
		target := localHTTPTarget(cfg.Addr)
		if target == "" {
			return fmt.Errorf("region tunnel: cannot derive local HTTP target from %q", cfg.Addr)
		}
		agent := tunnel.NewRegionAgent(tunnel.RegionAgentConfig{
			URL:    cfg.RegionHubURL,
			Region: cfg.RegionName,
			Target: target,
			Token: func() (string, error) {
				return region.SignIdentity(cfg.RegionKey, region.Identity{
					User:   auth.User{Username: "region", Subject: cfg.RegionName},
					Admin:  true,
					Region: cfg.RegionName,
				})
			},
		})
		go func() {
			if err := agent.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
				log.Printf("region tunnel stopped: %v", err)
			}
		}()
	}

	var backups *backup.Store
	backups, err = backup.New(backup.Config{
		Endpoint:  cfg.BackupS3Endpoint,
		Region:    cfg.BackupS3Region,
		Bucket:    cfg.BackupS3Bucket,
		AccessKey: cfg.BackupS3AccessKey,
		SecretKey: cfg.BackupS3SecretKey,
		Prefix:    cfg.BackupS3Prefix,
		UseSSL:    cfg.BackupS3UseSSL,
	})
	if err != nil {
		return fmt.Errorf("volume backups: %w", err)
	}
	if backups != nil {
		log.Printf("volume backups enabled (S3 bucket %s)", cfg.BackupS3Bucket)
	}

	srv := &server.Server{
		Workspaces:        manager,
		Backups:           backups,
		RBAC:              rbacStore,
		Users:             userStore,
		Shares:            shareStore,
		Metadata:          metadataStore,
		Access:            accessStore,
		Quota:             quotaStore,
		Metrics:           appMetrics,
		WebFS:             web.FS,
		Token:             cfg.Token,
		TokenAdmin:        cfg.TokenAdmin,
		Local:             editor,
		Forwards:          forwards,
		Regions:           hub,
		RegionTunnel:      regionTunnel,
		RegionMode:        cfg.RegionMode,
		RegionKey:         cfg.RegionKey,
		RegionName:        cfg.RegionName,
		HubURL:            cfg.RegionHubURL,
		SSHHost:           cfg.SSHPublicHost,
		SSHPort:           cfg.SSHPublicPort,
		SSHKeys:           keyring,
		SSHAdminKeys:      keys,
		SSHAllowUnmapped:  allowUnmapped,
		UserKeys:          userKeyStore,
		DesktopSecret:     desktopSecret,
		SSHKeyPath:        sshKeyPath,
		Windows:           windows,
		WindowsSSHKeyPath: windowsKeyPath,
		SSHEnabled:        cfg.SSHEnabled && (cfg.SSHAddr == "" || sshBastion != nil),
		SSHCA:             sshCA,
		SSHCertTTL:        cfg.SSHCertTTL,
		VSCodeExtension: extension.Metadata{
			ID:          cfg.VSCodeExtensionID,
			Version:     cfg.VSCodeExtensionVersion,
			ReleaseURL:  cfg.VSCodeExtensionReleaseURL,
			DownloadURL: cfg.VSCodeExtensionDownloadURL,
		},
		OIDC:            oidcAuth,
		OIDCRedirectURL: cfg.OIDCRedirectURL,

		ExternalAuth:             externalAuth,
		ExternalAuthURL:          cfg.ExternalAuthURL,
		ExternalAuthRedirectBase: cfg.ExternalAuthRedirectBase,
	}

	httpServer := &http.Server{
		Addr:              cfg.Addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("poddy listening on %s (namespace %s)", cfg.Addr, cfg.Namespace)
	return serve(ctx, httpServer)
}

func newEditor(cfg config.Config) *local.Local {
	if cfg.Editor == "" {
		return nil
	}
	editor, err := local.New(cfg.Editor)
	if err != nil {
		log.Printf("local editor launch disabled: %v", err)
		return nil
	}
	if editor.Editor != "" && editor.WindowsEditor == "" {
		if _, err := exec.LookPath(editor.Editor); err != nil {
			editor.Editor = ""
		}
	}
	if !editor.HasVSCode() && !editor.HasCursor() {
		log.Printf("local editor launch disabled: neither VS Code nor Cursor was found in PATH")
		return nil
	}
	return editor
}

func newExternalAuth(ctx context.Context, cfg config.Config, client kubernetes.Interface, metaStore metadata.Store) (*externalauth.Manager, error) {
	providers, err := externalauth.LoadProviders()
	if err != nil {
		return nil, fmt.Errorf("external auth: %w", err)
	}
	if len(providers) == 0 {
		return nil, nil
	}

	secret := cfg.ExternalAuthKey
	if secret == "" {
		secret = cfg.OIDCSessionKey
	}
	if secret == "" {
		secret = cfg.DesktopSecret
	}
	if secret == "" {
		secret = randomSecret()
		log.Printf("external auth: using an ephemeral encryption key; set PODDY_EXTERNAL_AUTH_KEY to keep tokens across restarts")
	}
	if cfg.ExternalAuthURL == "" {
		log.Printf("external auth: PODDY_EXTERNAL_AUTH_URL is empty; workspaces will use the portal URL captured at creation time")
	}

	k8sStore := externalauth.NewStore(client, cfg.Namespace, cfg.ExternalAuthSecret, secret)
	var store externalauth.TokenStore = k8sStore
	if pg, ok := metaStore.(*metadata.Postgres); ok {
		pgStore := pg.ExternalAuthStore(cfg.Namespace, secret)
		tokens, err := k8sStore.All(ctx)
		if err != nil {
			log.Printf("external auth: import Kubernetes Secret tokens: %v; continuing with the Secret store", err)
		} else if err := pgStore.SeedIfEmpty(ctx, tokens); err != nil {
			log.Printf("external auth: seed PostgreSQL tokens: %v; continuing with the Secret store", err)
		} else {
			store = pgStore
		}
	}
	log.Printf("external auth enabled (%d providers)", len(providers))
	return externalauth.NewManager(providers, store, secret), nil
}

func postgresShareSecret(cfg config.Config) string {
	for _, secret := range []string{cfg.ExternalAuthKey, cfg.OIDCSessionKey, cfg.DesktopSecret} {
		if strings.TrimSpace(secret) != "" {
			return secret
		}
	}
	return ""
}

// RunBastion starts only the SSH bastion. It is intended for deployments that
// scale the portal and bastion independently.
func RunBastion(ctx context.Context, args []string) error {
	cfg, err := config.Load(args)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	if cfg.SSHAddr == "" {
		return fmt.Errorf("ssh bastion: -ssh-addr is required")
	}
	if cfg.SSHRequireAgent && cfg.AgentAddr == "" {
		return fmt.Errorf("ssh bastion: -agent-addr is required when -ssh-require-agent=true")
	}

	sshCA, err := sshca.LoadOrGenerate(cfg.SSHCAKeyFile)
	if err != nil {
		return fmt.Errorf("ssh ca: %w", err)
	}
	if cfg.SSHCAKeyFile == "" {
		log.Printf("ssh ca: using an ephemeral key; set PODDY_SSH_CA_KEY_FILE to share the CA with the portal")
	}

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	metaStore, err := metadata.OpenWithRetry(ctx, cfg.DatabaseURL, cfg.DatabaseAutoMigrate, cfg.DatabaseConnectTimeout)
	if err != nil {
		log.Printf("metadata: PostgreSQL unavailable, continuing without audit: %v", err)
		metaStore = metadata.Nop{}
	}
	defer metaStore.Close()
	metadataEnabled := metaStore.AuditEnabled()
	if metadataEnabled {
		log.Printf("metadata: PostgreSQL audit enabled")
	}
	var accessStore access.Store
	if cfg.RegionMode && cfg.RegionHubURL != "" {
		hubClient := region.NewClient(region.Config{Name: cfg.RegionName, URL: cfg.RegionHubURL, Key: cfg.RegionKey})
		accessStore = accessremote.New(hubClient, region.Identity{
			User:  auth.User{Username: "region", Subject: cfg.RegionName},
			Admin: true,
		})
		log.Printf("workspace access: using hub control plane at %s", cfg.RegionHubURL)
	} else {
		accessStore = openAccessStore(metaStore, cfg.Namespace)
	}

	client, restConfig, err := k8s.New(cfg.Kubeconfig, cfg.Context, cfg.K8sQPS, cfg.K8sBurst)
	if err != nil {
		return fmt.Errorf("kubernetes: %w", err)
	}

	keys, keyring, _, err := loadSSHKeys(cfg, cfg.SSHAllowUnmappedKeys)
	if err != nil {
		return err
	}

	userKeyStore := userkeys.NewStore(client, cfg.Namespace, cfg.SSHUserKeysConfigMap)
	if stored, err := userKeyStore.List(ctx); err != nil {
		log.Printf("ssh user keys: initial load: %v", err)
	} else if err := keyring.Update(keys, userkeys.Owned(stored), cfg.SSHAllowUnmappedKeys); err != nil {
		return fmt.Errorf("ssh user keys: %w", err)
	}
	go userkeys.Watch(ctx, userKeyStore, keyring, keys, cfg.SSHAllowUnmappedKeys)

	manager := &k8s.Manager{
		Client:          client,
		Rest:            restConfig,
		Namespace:       cfg.Namespace,
		AuthorizedKeys:  keys,
		SSHKeys:         keyring,
		SSHCA:           sshCA,
		SSHRequireAgent: cfg.SSHRequireAgent,
	}

	agentKey := agentSecret(cfg)
	if agentKey == "" {
		log.Printf("workspace agent tunnel: using an ephemeral signing key; set PODDY_AGENT_KEY to match the portal")
	}
	signer := tunnel.NewTokenSigner(agentKey)
	broker := tunnel.NewBroker(signer)
	defer broker.Close()

	sshBastion, err := bastion.New(cfg.SSHAddr, manager, keyring, cfg.SSHHostKey)
	if err != nil {
		return fmt.Errorf("ssh bastion: %w", err)
	}
	sshBastion.Broker = broker
	sshBastion.RequireAgent = cfg.SSHRequireAgent
	sshBastion.CA = sshCA
	sshBastion.Access = accessStore
	if metadataEnabled {
		sshBastion.Audit = metaStore
	}
	if err := sshBastion.Listen(); err != nil {
		return fmt.Errorf("ssh bastion: %w", err)
	}
	defer sshBastion.Close()

	if cfg.AgentAddr != "" {
		agentServer, err := startAgentServer(ctx, cfg.AgentAddr, sshBastion.AgentHandler())
		if err != nil {
			return err
		}
		defer agentServer.Close()
	}

	log.Printf("ssh bastion listening on %s (namespace %s)", cfg.SSHAddr, cfg.Namespace)
	errCh := make(chan error, 1)
	go func() {
		errCh <- sshBastion.Serve()
	}()

	select {
	case <-ctx.Done():
		return nil
	case err := <-errCh:
		if errors.Is(err, net.ErrClosed) {
			return nil
		}
		return err
	}
}

// openAccessStore returns a PostgreSQL-backed access store when the metadata
// database is available. Without a database it falls back to an in-process
// store, which only works for a single replica and cannot be shared between the
// portal and a separate bastion process.
func openAccessStore(store metadata.Store, namespace string) access.Store {
	if pg, ok := store.(*metadata.Postgres); ok {
		return pg.Access(namespace)
	}
	log.Printf("workspace access: using in-memory store; set PODDY_DATABASE_URL to share approvals and presence across replicas")
	return access.NewMemory()
}

// openQuotaStore returns a PostgreSQL-backed quota store when the metadata
// database is available. Without a database it falls back to an in-process
// store, which is only suitable for a single replica.
func openQuotaStore(store metadata.Store, namespace string) quota.Store {
	if pg, ok := store.(*metadata.Postgres); ok {
		return pg.Quota(namespace)
	}
	log.Printf("quotas: using in-memory store; set PODDY_DATABASE_URL to persist user and team quotas")
	return quota.NewMemory()
}

// migrateQuotas imports quotas from the legacy RBAC ConfigMap into the quota
// store. When the store is persistent the ConfigMap entries are cleared so the
// database is the single source of truth. The import is skipped if the store
// already contains quotas, which keeps restarts from overwriting live changes.
func migrateQuotas(ctx context.Context, store quota.Store, rbacStore rbac.ConfigStore, persistent bool) error {
	cfg, err := rbacStore.Load(ctx)
	if err != nil {
		return err
	}
	hasLegacy := len(cfg.UserQuotas) > 0
	for _, team := range cfg.Teams {
		if team.Quota != nil && !team.Quota.IsZero() {
			hasLegacy = true
			break
		}
	}
	if !hasLegacy {
		return nil
	}
	if persistent {
		users, err := store.UserQuotas(ctx)
		if err != nil {
			return err
		}
		teams, err := store.TeamQuotas(ctx)
		if err != nil {
			return err
		}
		if len(users) > 0 || len(teams) > 0 {
			return nil
		}
	}
	for identity, value := range cfg.UserQuotas {
		if value.IsZero() {
			continue
		}
		if err := store.SetUserQuota(ctx, identity, value); err != nil {
			return err
		}
	}
	for name, team := range cfg.Teams {
		if team.Quota == nil || team.Quota.IsZero() {
			continue
		}
		if err := store.SetTeamQuota(ctx, name, *team.Quota); err != nil {
			return err
		}
	}
	if !persistent {
		return nil
	}
	_, err = rbacStore.Update(ctx, func(cfg *rbac.Config) error {
		cfg.UserQuotas = nil
		for name, team := range cfg.Teams {
			team.Quota = nil
			cfg.Teams[name] = team
		}
		return nil
	})
	return err
}

func syncAll(ctx context.Context, namespace string, store metadata.Store, rbacStore rbac.ConfigStore, userStore userstore.Directory, manager *k8s.Manager, catalog *templates.Catalog) error {
	cfg, err := rbacStore.Load(ctx)
	if err != nil {
		return fmt.Errorf("load rbac: %w", err)
	}
	if err := store.SyncRBAC(ctx, namespace, cfg); err != nil {
		return err
	}
	if err := store.SyncTemplates(ctx, namespace, catalog, cfg); err != nil {
		return err
	}

	if users, err := userStore.List(ctx); err != nil {
		log.Printf("metadata: list users: %v", err)
	} else {
		for _, user := range users {
			if err := store.SyncUser(ctx, namespace, user); err != nil {
				log.Printf("metadata: sync user %s: %v", metadata.UserIdentity(user), err)
			}
		}
	}

	workspaces, err := manager.List(ctx)
	if err != nil {
		return fmt.Errorf("list workspaces: %w", err)
	}
	names := make([]string, 0, len(workspaces))
	for _, workspace := range workspaces {
		names = append(names, workspace.Name)
		snapshot, err := manager.Snapshot(ctx, workspace.Name)
		if err != nil {
			log.Printf("metadata: snapshot workspace %s: %v", workspace.Name, err)
			continue
		}
		if err := store.SyncWorkspace(ctx, namespace, snapshot); err != nil {
			log.Printf("metadata: sync workspace %s: %v", workspace.Name, err)
		}
	}
	if err := store.PruneWorkspaces(ctx, namespace, names); err != nil {
		return err
	}
	return nil
}

func syncTemplates(ctx context.Context, namespace string, store metadata.Store, rbacStore rbac.ConfigStore, catalog *templates.Catalog) error {
	cfg, err := rbacStore.Load(ctx)
	if err != nil {
		return fmt.Errorf("load rbac: %w", err)
	}
	return store.SyncTemplates(ctx, namespace, catalog, cfg)
}

func loadSSHKeys(cfg config.Config, allowUnmapped bool) ([]string, *sshkeys.Dynamic, *local.Local, error) {
	editor := newEditor(cfg)
	keys, err := collectKeys(editor, cfg.SSHKeys, cfg.SSHKeysFile)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("ssh keys: %w", err)
	}
	keyring, err := sshkeys.NewDynamic(keys, nil, allowUnmapped)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("ssh keys: %w", err)
	}
	return keys, keyring, editor, nil
}

func startBastion(ctx context.Context, cfg config.Config, manager *k8s.Manager, keys sshkeys.Keyring, audit metadata.Auditor, signer *tunnel.TokenSigner, ca *sshca.CA, accessStore access.Store) (*bastion.Bastion, error) {
	if cfg.SSHAddr == "" {
		return nil, nil
	}
	if cfg.SSHRequireAgent && cfg.AgentAddr == "" {
		return nil, fmt.Errorf("ssh bastion: -agent-addr is required when -ssh-require-agent=true")
	}
	broker := tunnel.NewBroker(signer)
	sshBastion, err := bastion.New(cfg.SSHAddr, manager, keys, cfg.SSHHostKey)
	if err != nil {
		broker.Close()
		return nil, fmt.Errorf("ssh bastion: %w", err)
	}
	sshBastion.Audit = audit
	sshBastion.Broker = broker
	sshBastion.RequireAgent = cfg.SSHRequireAgent
	sshBastion.CA = ca
	sshBastion.Access = accessStore
	if err := sshBastion.Listen(); err != nil {
		broker.Close()
		return nil, fmt.Errorf("ssh bastion: %w", err)
	}
	if cfg.AgentAddr != "" {
		if _, err := startAgentServer(ctx, cfg.AgentAddr, sshBastion.AgentHandler()); err != nil {
			_ = sshBastion.Close()
			return nil, err
		}
	}
	go func() {
		log.Printf("ssh bastion listening on %s", cfg.SSHAddr)
		if err := sshBastion.Serve(); err != nil && !errors.Is(err, net.ErrClosed) {
			log.Printf("ssh bastion: %v", err)
		}
	}()
	return sshBastion, nil
}

func newOIDC(ctx context.Context, cfg config.Config) (*auth.OIDC, error) {
	if cfg.OIDCIssuer == "" && cfg.OIDCClientID == "" {
		return nil, nil
	}

	discoveryCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	oidcAuth, err := auth.New(discoveryCtx, auth.Config{
		Issuer:           cfg.OIDCIssuer,
		DiscoveryURL:     cfg.OIDCDiscoveryURL,
		AuthorizationURL: cfg.OIDCAuthorizationURL,
		ClientID:         cfg.OIDCClientID,
		ClientSecret:     cfg.OIDCClientSecret,
		RedirectURL:      cfg.OIDCRedirectURL,
		Scopes:           splitList(cfg.OIDCScopes),
		AllowedGroups:    splitList(cfg.OIDCAllowedGroups),
		AllowedUsers:     splitList(cfg.OIDCAllowedUsers),
		UsernameClaim:    cfg.OIDCUsernameClaim,
		EmailClaim:       cfg.OIDCEmailClaim,
		GroupsClaim:      cfg.OIDCGroupsClaim,
		SessionKey:       []byte(cfg.OIDCSessionKey),
		SessionTTL:       cfg.OIDCSessionTTL,
	})
	if err != nil {
		return nil, fmt.Errorf("oidc: %w", err)
	}
	log.Printf("oidc login enabled (issuer %s)", cfg.OIDCIssuer)
	return oidcAuth, nil
}

func startMetrics(ctx context.Context, addr string, handler http.Handler) (*http.Server, error) {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("metrics: %w", err)
	}
	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		log.Printf("metrics listening on %s", addr)
		if err := srv.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("metrics: %v", err)
		}
	}()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	return srv, nil
}

func serve(ctx context.Context, srv *http.Server) error {
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("http shutdown: %w", err)
		}
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func syncRegionKeys(ctx context.Context, hub *region.Hub, store *userkeys.Store, interval time.Duration, trigger <-chan struct{}) {
	sync := func() {
		keys, err := store.List(ctx)
		if err != nil {
			log.Printf("region ssh keys: list: %v", err)
			return
		}
		byOwner := map[string][]string{}
		for _, key := range keys {
			byOwner[key.Owner] = append(byOwner[key.Owner], key.Line)
		}
		identity := region.Identity{User: auth.User{Username: "hub", Subject: "hub"}, Admin: true}
		for owner, lines := range byOwner {
			if problems := hub.SyncSSHKeys(ctx, owner, lines, identity); len(problems) > 0 {
				for _, problem := range problems {
					log.Printf("region ssh keys: %v", problem)
				}
			}
		}
	}
	sync()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sync()
		case <-trigger:
			sync()
		}
	}
}
