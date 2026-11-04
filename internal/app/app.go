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
	"syscall"
	"time"

	"k8s.io/client-go/kubernetes"

	"poddy/internal/auth"
	"poddy/internal/bastion"
	"poddy/internal/config"
	"poddy/internal/externalauth"
	"poddy/internal/k8s"
	"poddy/internal/local"
	"poddy/internal/rbac"
	"poddy/internal/server"
	"poddy/internal/sshkeys"
	"poddy/internal/templates"
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

	externalAuth, err := newExternalAuth(cfg, client)
	if err != nil {
		return err
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
		PortalURL:       cfg.ExternalAuthURL,
	}
	if externalAuth != nil {
		manager.ExternalAuth = func(ctx context.Context, owner, _ string, _ string, requirements []templates.ExternalAuth) (map[string]string, error) {
			return externalAuth.Prepare(ctx, strings.ToLower(strings.TrimSpace(owner)), requirements)
		}
		manager.ExternalAuthToken = externalAuth.SignWorkspace
	}

	sshBastion, err := startBastion(cfg, manager, keyring)
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

	srv := &server.Server{
		Workspaces:        manager,
		RBAC:              rbac.NewStore(client, cfg.Namespace, cfg.RBACConfigMap),
		Users:             userstore.NewStore(client, cfg.Namespace, userstore.DefaultConfigMap),
		WebFS:             web.FS,
		Token:             cfg.Token,
		Local:             editor,
		Forwards:          forwards,
		SSHHost:           cfg.SSHPublicHost,
		SSHPort:           cfg.SSHPublicPort,
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

	client, restConfig, err := k8s.New(cfg.Kubeconfig, cfg.Context)
	if err != nil {
		return fmt.Errorf("kubernetes: %w", err)
	}

	keys, keyring, _, err := loadSSHKeys(cfg, cfg.SSHAllowUnmappedKeys)
	if err != nil {
		return err
	}
	if keyring.Len() == 0 {
		return fmt.Errorf("ssh bastion: no authorized keys configured")
	}

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
	if err := sshBastion.Listen(); err != nil {
		return fmt.Errorf("ssh bastion: %w", err)
	}
	defer sshBastion.Close()

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

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

func loadSSHKeys(cfg config.Config, allowUnmapped bool) ([]string, *sshkeys.Set, *local.Local, error) {
	editor := newEditor(cfg)
	keys, err := collectKeys(editor, cfg.SSHKeys, cfg.SSHKeysFile)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("ssh keys: %w", err)
	}
	keyring, err := sshkeys.Parse(keys, allowUnmapped)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("ssh keys: %w", err)
	}
	return keys, keyring, editor, nil
}

func startBastion(cfg config.Config, manager *k8s.Manager, keys *sshkeys.Set) (*bastion.Bastion, error) {
	if cfg.SSHAddr == "" {
		return nil, nil
	}
	if keys.Len() == 0 {
		log.Printf("ssh bastion disabled: no authorized keys")
		return nil, nil
	}

	sshBastion, err := bastion.New(cfg.SSHAddr, manager, keys, cfg.SSHHostKey)
	if err != nil {
		return nil, fmt.Errorf("ssh bastion: %w", err)
	}
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
