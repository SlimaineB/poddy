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

	"poddy/internal/auth"
	"poddy/internal/backup"
	"poddy/internal/bastion"
	"poddy/internal/config"
	"poddy/internal/externalauth"
	"poddy/internal/k8s"
	"poddy/internal/local"
	"poddy/internal/metadata"
	"poddy/internal/metrics"
	"poddy/internal/rbac"
	"poddy/internal/region"
	"poddy/internal/server"
	"poddy/internal/sshkeys"
	"poddy/internal/templates"
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

	var appMetrics *metrics.Metrics
	if cfg.MetricsAddr != "" {
		appMetrics = metrics.New()
	}

	metaStore, err := metadata.Open(ctx, cfg.DatabaseURL, cfg.DatabaseAutoMigrate)
	if err != nil {
		return fmt.Errorf("metadata: %w", err)
	}
	defer metaStore.Close()
	if cfg.DatabaseURL != "" {
		log.Printf("metadata: PostgreSQL mirror and audit enabled")
	}

	client, restConfig, err := k8s.New(cfg.Kubeconfig, cfg.Context)
	if err != nil {
		return fmt.Errorf("kubernetes: %w", err)
	}

	catalog, err := templates.Load(cfg.TemplatesDir)
	if err != nil {
		return fmt.Errorf("templates: %w", err)
	}

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	rbacStore := rbac.NewStore(client, cfg.Namespace, cfg.RBACConfigMap)
	userStore := userstore.NewStore(client, cfg.Namespace, userstore.DefaultConfigMap)
	if appMetrics != nil || cfg.DatabaseURL != "" {
		catalog.SetOnReload(func() {
			appMetrics.TemplateReload("success")
			if cfg.DatabaseURL == "" {
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

	externalAuth, err := newExternalAuth(cfg, client)
	if err != nil {
		return err
	}

	portalURL := cfg.ExternalAuthURL
	if cfg.RegionMode && cfg.RegionHubURL != "" {
		portalURL = cfg.RegionHubURL
	}
	manager := &k8s.Manager{
		Client:          client,
		Rest:            restConfig,
		Namespace:       cfg.Namespace,
		Templates:       catalog,
		DefaultTemplate: cfg.DefaultTemplate,
		AuthorizedKeys:  keys,
		SSHKeys:         keyring,
		StateConfigMap:  cfg.StateConfigMap,
		PortalURL:       portalURL,
		InstanceID:      cfg.InstanceID,
		Region:          cfg.RegionName,
		HelperImage:     cfg.BackupHelperImage,
	}
	if externalAuth != nil {
		manager.ExternalAuth = func(ctx context.Context, owner, _ string, _ string, requirements []templates.ExternalAuth) (map[string]string, error) {
			return externalAuth.Prepare(ctx, strings.ToLower(strings.TrimSpace(owner)), requirements)
		}
		manager.ExternalAuthToken = externalAuth.SignWorkspace
	}
	if cfg.RegionMode && cfg.RegionHubURL != "" {
		hubClient := region.NewClient(region.Config{Name: cfg.RegionName, URL: cfg.RegionHubURL, Key: cfg.RegionKey})
		var tokens sync.Map
		manager.ExternalAuth = func(ctx context.Context, owner, workspace, portalURL string, requirements []templates.ExternalAuth) (map[string]string, error) {
			response, err := hubClient.FetchExternalAuth(ctx, region.ExternalAuthRequest{
				Owner:        owner,
				Workspace:    workspace,
				PortalURL:    portalURL,
				Requirements: requirements,
			}, region.Identity{User: auth.User{Username: "region", Subject: cfg.RegionName}, Admin: true})
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

	var metadataStore metadata.Store
	var metadataAuditor metadata.Auditor
	if cfg.DatabaseURL != "" {
		metadataStore = metaStore
		metadataAuditor = metaStore
		if err := syncAll(ctx, cfg.Namespace, metaStore, rbacStore, userStore, manager, catalog); err != nil {
			return fmt.Errorf("metadata sync: %w", err)
		}
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

	sshBastion, err := startBastion(cfg, manager, keyring, metadataAuditor)
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
	if len(registry.All()) > 0 {
		if cfg.RegionMode {
			return fmt.Errorf("regions: region mode cannot also act as a central hub")
		}
		hub = region.NewHub(registry)
		log.Printf("central hub configured with %d regions", len(registry.All()))
		go syncRegionKeys(ctx, hub, userKeyStore, 2*time.Minute)
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
		Metadata:          metadataStore,
		Metrics:           appMetrics,
		WebFS:             web.FS,
		Token:             cfg.Token,
		TokenAdmin:        cfg.TokenAdmin,
		Local:             editor,
		Forwards:          forwards,
		Regions:           hub,
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
		OIDC:              oidcAuth,
		OIDCRedirectURL:   cfg.OIDCRedirectURL,

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
		log.Printf("VS Code launch disabled: %v", err)
		return nil
	}
	if editor.WindowsEditor == "" {
		if _, err := exec.LookPath(cfg.Editor); err != nil {
			log.Printf("VS Code launch disabled: %q not found in PATH", cfg.Editor)
			return nil
		}
	}
	return editor
}

func newExternalAuth(cfg config.Config, client kubernetes.Interface) (*externalauth.Manager, error) {
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
		secret = randomSecret()
		log.Printf("external auth: using an ephemeral encryption key; set PODDY_EXTERNAL_AUTH_KEY to keep tokens across restarts")
	}
	if cfg.ExternalAuthURL == "" {
		log.Printf("external auth: PODDY_EXTERNAL_AUTH_URL is empty; workspaces will use the portal URL captured at creation time")
	}

	store := externalauth.NewStore(client, cfg.Namespace, cfg.ExternalAuthSecret, secret)
	log.Printf("external auth enabled (%d providers)", len(providers))
	return externalauth.NewManager(providers, store, secret), nil
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

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	metaStore, err := metadata.Open(ctx, cfg.DatabaseURL, cfg.DatabaseAutoMigrate)
	if err != nil {
		return fmt.Errorf("metadata: %w", err)
	}
	defer metaStore.Close()
	if cfg.DatabaseURL != "" {
		log.Printf("metadata: PostgreSQL audit enabled")
	}

	client, restConfig, err := k8s.New(cfg.Kubeconfig, cfg.Context)
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
		Client:         client,
		Rest:           restConfig,
		Namespace:      cfg.Namespace,
		AuthorizedKeys: keys,
		SSHKeys:        keyring,
	}

	sshBastion, err := bastion.New(cfg.SSHAddr, manager, keyring, cfg.SSHHostKey)
	if err != nil {
		return fmt.Errorf("ssh bastion: %w", err)
	}
	if cfg.DatabaseURL != "" {
		sshBastion.Audit = metaStore
	}
	if err := sshBastion.Listen(); err != nil {
		return fmt.Errorf("ssh bastion: %w", err)
	}
	defer sshBastion.Close()

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

func syncAll(ctx context.Context, namespace string, store metadata.Store, rbacStore *rbac.Store, userStore *userstore.Store, manager *k8s.Manager, catalog *templates.Catalog) error {
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

func syncTemplates(ctx context.Context, namespace string, store metadata.Store, rbacStore *rbac.Store, catalog *templates.Catalog) error {
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

func startBastion(cfg config.Config, manager *k8s.Manager, keys sshkeys.Keyring, audit metadata.Auditor) (*bastion.Bastion, error) {
	if cfg.SSHAddr == "" {
		return nil, nil
	}
	sshBastion, err := bastion.New(cfg.SSHAddr, manager, keys, cfg.SSHHostKey)
	if err != nil {
		return nil, fmt.Errorf("ssh bastion: %w", err)
	}
	sshBastion.Audit = audit
	if err := sshBastion.Listen(); err != nil {
		return nil, fmt.Errorf("ssh bastion: %w", err)
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
		Issuer:        cfg.OIDCIssuer,
		ClientID:      cfg.OIDCClientID,
		ClientSecret:  cfg.OIDCClientSecret,
		RedirectURL:   cfg.OIDCRedirectURL,
		Scopes:        splitList(cfg.OIDCScopes),
		AllowedGroups: splitList(cfg.OIDCAllowedGroups),
		AllowedUsers:  splitList(cfg.OIDCAllowedUsers),
		UsernameClaim: cfg.OIDCUsernameClaim,
		EmailClaim:    cfg.OIDCEmailClaim,
		GroupsClaim:   cfg.OIDCGroupsClaim,
		SessionKey:    []byte(cfg.OIDCSessionKey),
		SessionTTL:    cfg.OIDCSessionTTL,
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

func syncRegionKeys(ctx context.Context, hub *region.Hub, store *userkeys.Store, interval time.Duration) {
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
		}
	}
}
