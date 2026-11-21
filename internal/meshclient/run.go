package meshclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"tailscale.com/net/netns"

	"poddy/internal/mesh"
)

type Config struct {
	ControlURL string
	AuthKey    string
	Workspace  string
	Hostname   string
	StateDir   string
	CAFile     string
	Port       int
}

func Run(ctx context.Context, cfg Config, stdin io.Reader, stdout io.Writer) error {
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
	stateDir := strings.TrimSpace(cfg.StateDir)
	if stateDir == "" {
		cache, err := os.UserCacheDir()
		if err != nil {
			return fmt.Errorf("mesh state directory: %w", err)
		}
		stateDir = filepath.Join(cache, "poddy", "mesh", mesh.SanitizeHostname(cfg.Workspace))
	}
	port := cfg.Port
	if port == 0 {
		port = 22
	}
	netns.SetEnabled(false)
	startCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	node, err := mesh.Start(startCtx, mesh.Config{
		ControlURL: cfg.ControlURL,
		AuthKey:    cfg.AuthKey,
		Hostname:   hostname,
		StateDir:   stateDir,
		Ephemeral:  false,
		Silent:     os.Getenv("PODDY_MESH_DEBUG") == "",
		RandomPort: true,
		CACert:     caCert,
	})
	cancel()
	if err != nil {
		if errors.Is(startCtx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("mesh: join tailnet timed out after 60s: %w", err)
		}
		return err
	}
	defer node.Close()

	conn, err := node.DialWorkspace(ctx, cfg.Workspace, port)
	if err != nil {
		return fmt.Errorf("mesh dial %s: %w", cfg.Workspace, err)
	}
	defer conn.Close()

	done := make(chan error, 2)
	go func() { _, err := io.Copy(stdout, conn); done <- err }()
	go func() { _, err := io.Copy(conn, stdin); done <- err }()
	select {
	case err := <-done:
		if err == nil || errors.Is(err, io.EOF) {
			return nil
		}
		return err
	case <-ctx.Done():
		return nil
	}
}
