package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"poddy/internal/mesh"
	"poddy/internal/tunnel"
)

func main() {
	url := flag.String("url", os.Getenv("PODDY_AGENT_URL"), "portal tunnel URL")
	token := flag.String("token", os.Getenv("PODDY_AGENT_TOKEN"), "workspace agent token")
	tokenFile := flag.String("token-file", os.Getenv("PODDY_AGENT_TOKEN_FILE"), "file used to persist a renewed agent token")
	refreshFile := flag.String("refresh-file", os.Getenv("PODDY_AGENT_REFRESH_FILE"), "mounted Secret file refreshed by the control plane")
	workspace := flag.String("workspace", os.Getenv("PODDY_WORKSPACE_NAME"), "workspace name")
	target := flag.String("target", env("PODDY_AGENT_TARGET", "127.0.0.1:22"), "local TCP target")
	insecure := flag.Bool("insecure", os.Getenv("PODDY_AGENT_INSECURE") == "true", "skip TLS verification (development only)")
	meshControlURL := flag.String("mesh-control-url", os.Getenv("PODDY_MESH_CONTROL_URL"), "mesh control server URL")
	meshAuthKey := flag.String("mesh-auth-key", os.Getenv("PODDY_MESH_AUTH_KEY"), "mesh auth key")
	meshStateDir := flag.String("mesh-state-dir", env("PODDY_MESH_STATE_DIR", "/var/lib/poddy/tsnet"), "writable mesh state directory")
	meshCACert := flag.String("mesh-ca-cert", os.Getenv("PODDY_MESH_CA_CERT"), "mesh control/DERP CA bundle (PEM)")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	name := strings.TrimSpace(*workspace)
	if name == "" {
		log.Fatal("workspace name is required")
	}
	if *url == "" && strings.TrimSpace(*meshAuthKey) == "" {
		log.Fatal("either PODDY_AGENT_URL or PODDY_MESH_AUTH_KEY is required")
	}
	log.Printf("workspace agent starting for %s", safe(name))

	health := newAgentHealth()
	healthAddr := env("PODDY_AGENT_HEALTH_ADDR", ":8081")
	go func() {
		if err := health.run(ctx, healthAddr); err != nil && ctx.Err() == nil {
			log.Printf("health server: %v", err)
		}
	}()

	errors := make(chan error, 2)
	if *url != "" {
		agent := tunnel.NewAgent(tunnel.AgentConfig{
			URL:         *url,
			Token:       *token,
			TokenFile:   *tokenFile,
			RefreshFile: *refreshFile,
			Workspace:   name,
			Target:      *target,
			Insecure:    *insecure,
		})
		go func() { errors <- agent.Run(ctx) }()
	}
	if strings.TrimSpace(*meshAuthKey) != "" {
		go func() {
			errors <- runMesh(ctx, health, name, *meshControlURL, *meshAuthKey, *meshStateDir, *meshCACert, *target)
		}()
	} else {
		health.setReady(true, "")
	}

	select {
	case err := <-errors:
		if err != nil && ctx.Err() == nil {
			log.Fatal(err)
		}
	case <-ctx.Done():
	}
}

func runMesh(ctx context.Context, health *agentHealth, workspace, controlURL, authKey, stateDir, caCert, target string) error {
	defer health.setReady(false, "mesh stopped")
	node, err := mesh.Start(ctx, mesh.Config{
		ControlURL: controlURL,
		AuthKey:    authKey,
		Hostname:   mesh.WorkspaceHostname(workspace),
		StateDir:   stateDir,
		Ephemeral:  true,
		RandomPort: true,
		CACert:     caCert,
	})
	if err != nil {
		return err
	}
	defer node.Close()

	listener, err := node.Listen("tcp", ":22")
	if err != nil {
		return fmt.Errorf("mesh listen: %w", err)
	}
	defer listener.Close()
	log.Printf("workspace %s joined the mesh as %s", safe(workspace), mesh.WorkspaceHostname(workspace))
	health.setReady(true, "")

	unhealthy := make(chan error, 1)
	go monitorMesh(ctx, node, health, meshUnhealthyTimeout(), func(err error) {
		unhealthy <- err
		_ = listener.Close()
	})

	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			select {
			case monitorErr := <-unhealthy:
				return monitorErr
			default:
			}
			return err
		}
		go forward(conn, target)
	}
}

// monitorMesh keeps the Kubernetes readiness signal tied to the actual
// WireGuard backend, not just to the process being alive. If the backend stays
// down past the configured budget, the agent exits and Kubernetes restarts it
// with backoff; this recovers from a wedged tsnet state that would otherwise
// leave a Running-but-unreachable workspace forever.
func monitorMesh(ctx context.Context, node *mesh.Node, health *agentHealth, unhealthyAfter time.Duration, onUnhealthy func(error)) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	var unhealthySince time.Time
	for {
		checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := node.Ready(checkCtx)
		cancel()
		if err == nil {
			health.setReady(true, "")
			unhealthySince = time.Time{}
		} else {
			health.setReady(false, err.Error())
			if unhealthySince.IsZero() {
				unhealthySince = time.Now()
			}
			if unhealthyAfter > 0 && time.Since(unhealthySince) >= unhealthyAfter {
				onUnhealthy(fmt.Errorf("mesh: backend unhealthy for %s: %w", unhealthyAfter, err))
				return
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func meshUnhealthyTimeout() time.Duration {
	raw := strings.TrimSpace(os.Getenv("PODDY_MESH_UNHEALTHY_TIMEOUT"))
	if raw == "" {
		return time.Minute
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value < 0 {
		log.Printf("invalid PODDY_MESH_UNHEALTHY_TIMEOUT %q; using 1m", safe(raw))
		return time.Minute
	}
	return value
}

func forward(conn net.Conn, target string) {
	defer conn.Close()
	upstream, err := net.Dial("tcp", target)
	if err != nil {
		log.Printf("mesh forward %s: %v", target, err)
		return
	}
	defer upstream.Close()

	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(upstream, conn)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(conn, upstream)
		done <- struct{}{}
	}()
	<-done
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func safe(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "\n", " "), "\r", " ")
}
