package meshclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"tailscale.com/net/netns"

	"poddy/internal/mesh"
)

type Config struct {
	ControlURL  string
	AuthKey     string
	Workspace   string
	Hostname    string
	StateDir    string
	CAFile      string
	Port        int
	JoinTimeout time.Duration
}

func NormalizeWorkspace(name string) string {
	return strings.TrimPrefix(strings.TrimSpace(name), "poddy-desktop-")
}

func Run(ctx context.Context, cfg Config, stdin io.Reader, stdout io.Writer) error {
	cfg.Workspace = NormalizeWorkspace(cfg.Workspace)
	if strings.TrimSpace(cfg.ControlURL) == "" {
		return errors.New("mesh control URL is required")
	}
	if strings.TrimSpace(cfg.Workspace) == "" {
		return errors.New("workspace is required")
	}
	if strings.TrimSpace(cfg.AuthKey) == "" {
		return errors.New("mesh auth key is required")
	}
	caCert := ""
	if strings.TrimSpace(cfg.CAFile) != "" {
		pem, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return fmt.Errorf("read CA file: %w", err)
		}
		caCert = string(pem)
	}
	hostname := strings.TrimSpace(cfg.Hostname)
	if hostname == "" {
		hostname = "poddy-client-" + mesh.SanitizeHostname(cfg.Workspace)
	}
	// Each ProxyCommand invocation gets its own ephemeral node so repeated
	// attempts do not reuse the same Headscale node and flap map sessions.
	hostname = mesh.SanitizeHostname(hostname + "-" + strconv.Itoa(os.Getpid()))
	stateDir := strings.TrimSpace(cfg.StateDir)
	if stateDir == "" {
		cache, err := os.UserCacheDir()
		if err != nil {
			return fmt.Errorf("mesh state directory: %w", err)
		}
		stateDir = filepath.Join(cache, "poddy", "mesh", mesh.SanitizeHostname(cfg.Workspace))
	}
	// Never share tsnet state between concurrent ProxyCommand invocations. Each
	// one gets a throwaway node, and a stale lock from a previous attempt must
	// not block the next connection.
	stateDir = filepath.Join(stateDir, hostname)
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return fmt.Errorf("mesh state directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(stateDir) }()
	port := cfg.Port
	if port == 0 {
		port = 22
	}
	netns.SetEnabled(false)
	if os.Getenv("TSNET_FORCE_LOGIN") == "" {
		os.Setenv("TSNET_FORCE_LOGIN", "1")
	}
	joinTimeout := cfg.JoinTimeout
	if joinTimeout <= 0 {
		joinTimeout = 60 * time.Second
	}
	startCtx, cancel := context.WithTimeout(ctx, joinTimeout)
	node, err := mesh.Start(startCtx, mesh.Config{
		ControlURL: cfg.ControlURL,
		AuthKey:    cfg.AuthKey,
		Hostname:   hostname,
		StateDir:   stateDir,
		Ephemeral:  true,
		Silent:     os.Getenv("PODDY_MESH_DEBUG") == "",
		RandomPort: true,
		CACert:     caCert,
	})
	cancel()
	if err != nil {
		if errors.Is(startCtx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("mesh: join tailnet timed out after %s: %w", joinTimeout, err)
		}
		return err
	}
	defer node.Close()

	conn, err := node.DialWorkspace(ctx, cfg.Workspace, port)
	if err != nil {
		return fmt.Errorf("mesh dial %s: %w", cfg.Workspace, err)
	}
	defer conn.Close()

	// Remote-SSH pipes a startup script on stdin and then keeps the session open
	// for dynamic forwarding (-D). A stdin EOF is therefore normal half-close,
	// not a reason to close the whole proxy tunnel: only stdout ending or the
	// context closing should terminate the bridge.
	stdoutDone := make(chan error, 1)
	go func() { _, err := io.Copy(stdout, conn); stdoutDone <- err }()
	go func() { _, _ = io.Copy(conn, stdin) }()
	select {
	case err := <-stdoutDone:
		if err == nil || errors.Is(err, io.EOF) {
			return nil
		}
		return err
	case <-ctx.Done():
		return nil
	}
}
