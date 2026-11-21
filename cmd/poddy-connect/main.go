// Command poddy-connect is the ProxyCommand used by the Poddy desktop editor
// extension. It carries the SSH transport over a WebSocket to the portal.
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/gorilla/websocket"

	"poddy/internal/mesh"
)

func main() {
	log.SetFlags(0)
	log.SetPrefix("poddy-connect: ")
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func run() error {
	portal := flag.String("portal", "", "portal URL, for example https://poddy.example.com")
	workspace := flag.String("workspace", "", "workspace name")
	ticket := flag.String("ticket", "", "desktop connection ticket")
	ticketFile := flag.String("ticket-file", "", "file containing the desktop connection ticket")
	mode := flag.String("mode", "", "connection mode: portal or mesh")
	controlURL := flag.String("control-url", "", "mesh control server URL")
	authKey := flag.String("auth-key", "", "mesh auth key")
	authKeyFile := flag.String("auth-key-file", "", "file containing the mesh auth key")
	stateDir := flag.String("state-dir", "", "writable mesh state directory")
	hostname := flag.String("hostname", "", "mesh hostname for this client")
	port := flag.Int("port", 22, "workspace SSH port")
	caFile := flag.String("ca-file", "", "additional CA bundle for a private portal or mesh certificate")
	insecure := flag.Bool("insecure", false, "disable portal TLS certificate verification")
	timeout := flag.Duration("timeout", 20*time.Second, "WebSocket handshake timeout")
	flag.Parse()

	if strings.TrimSpace(*workspace) == "" {
		return errors.New("--workspace is required")
	}
	if strings.EqualFold(strings.TrimSpace(*mode), "mesh") || strings.TrimSpace(*controlURL) != "" {
		return runMeshBridge(*controlURL, *workspace, *hostname, *authKey, *authKeyFile, *stateDir, *caFile, *port)
	}
	if strings.TrimSpace(*portal) == "" {
		return errors.New("--portal is required")
	}
	connectionTicket := strings.TrimSpace(*ticket)
	if connectionTicket == "" && strings.TrimSpace(*ticketFile) != "" {
		data, err := os.ReadFile(*ticketFile)
		if err != nil {
			return fmt.Errorf("read ticket file: %w", err)
		}
		connectionTicket = strings.TrimSpace(string(data))
	}
	if connectionTicket == "" {
		return errors.New("--ticket or --ticket-file is required")
	}
	target, err := sshURL(*portal, *workspace, connectionTicket)
	if err != nil {
		return err
	}

	dialer := websocket.Dialer{
		HandshakeTimeout: *timeout,
		Proxy:            http.ProxyFromEnvironment,
	}
	if target.Scheme == "wss" {
		tlsConfig, err := portalTLSConfig(*caFile, *insecure)
		if err != nil {
			return err
		}
		dialer.TLSClientConfig = tlsConfig
	}

	ws, resp, err := dialer.Dial(target.String(), nil)
	if err != nil {
		if resp != nil {
			return fmt.Errorf("portal returned %s: %w", resp.Status, err)
		}
		return err
	}
	defer ws.Close()
	ws.SetReadLimit(1 << 20)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(stop)

	done := make(chan error, 2)
	go func() {
		done <- copyWebSocketToStdout(ws)
	}()
	go func() {
		done <- copyStdinToWebSocket(ws)
	}()

	select {
	case err := <-done:
		if err == nil || isExpectedClose(err) {
			return nil
		}
		return err
	case <-stop:
		_ = ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
		return nil
	}
}

func sshURL(portal, workspace, ticket string) (*url.URL, error) {
	target, err := url.Parse(strings.TrimSpace(portal))
	if err != nil {
		return nil, fmt.Errorf("invalid portal URL: %w", err)
	}
	switch target.Scheme {
	case "http":
		target.Scheme = "ws"
	case "https":
		target.Scheme = "wss"
	case "ws", "wss":
	default:
		return nil, fmt.Errorf("unsupported portal scheme %q", target.Scheme)
	}
	target.Path = "/api/desktop/ssh"
	target.RawPath = ""
	target.RawQuery = ""
	target.Fragment = ""
	query := target.Query()
	query.Set("workspace", workspace)
	query.Set("ticket", ticket)
	target.RawQuery = query.Encode()
	return target, nil
}

func portalTLSConfig(caFile string, insecure bool) (*tls.Config, error) {
	config := &tls.Config{MinVersion: tls.VersionTLS12}
	if insecure {
		config.InsecureSkipVerify = true
	}
	if strings.TrimSpace(caFile) == "" {
		return config, nil
	}
	pem, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("read CA file: %w", err)
	}
	roots, err := x509.SystemCertPool()
	if err != nil || roots == nil {
		roots = x509.NewCertPool()
	}
	if !roots.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("CA file %q contains no certificates", caFile)
	}
	config.RootCAs = roots
	return config, nil
}

func copyWebSocketToStdout(ws *websocket.Conn) error {
	for {
		_, reader, err := ws.NextReader()
		if err != nil {
			return err
		}
		if _, err := io.Copy(os.Stdout, reader); err != nil {
			return err
		}
	}
}

func copyStdinToWebSocket(ws *websocket.Conn) error {
	buf := make([]byte, 32<<10)
	for {
		n, err := os.Stdin.Read(buf)
		if n > 0 {
			if writeErr := ws.WriteMessage(websocket.BinaryMessage, buf[:n]); writeErr != nil {
				return writeErr
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				_ = ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
				return nil
			}
			return err
		}
	}
}

func runMeshBridge(controlURL, workspace, hostname, authKey, authKeyFile, stateDir, caFile string, port int) error {
	if strings.TrimSpace(controlURL) == "" {
		return errors.New("--control-url is required in mesh mode")
	}
	key := strings.TrimSpace(authKey)
	if key == "" && strings.TrimSpace(authKeyFile) != "" {
		data, err := os.ReadFile(authKeyFile)
		if err != nil {
			return fmt.Errorf("read auth key file: %w", err)
		}
		key = strings.TrimSpace(string(data))
	}
	if key == "" {
		return errors.New("--auth-key or --auth-key-file is required in mesh mode")
	}
	if strings.TrimSpace(caFile) != "" {
		_ = os.Setenv("SSL_CERT_FILE", caFile)
	}
	if strings.TrimSpace(hostname) == "" {
		hostname = "poddy-client-" + mesh.SanitizeHostname(workspace)
	}
	if strings.TrimSpace(stateDir) == "" {
		cache, err := os.UserCacheDir()
		if err != nil {
			return fmt.Errorf("mesh state directory: %w", err)
		}
		stateDir = filepath.Join(cache, "poddy", "mesh", mesh.SanitizeHostname(workspace))
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	node, err := mesh.Start(ctx, mesh.Config{
		ControlURL: controlURL,
		AuthKey:    key,
		Hostname:   hostname,
		StateDir:   stateDir,
		Ephemeral:  false,
	})
	if err != nil {
		return err
	}
	defer node.Close()

	conn, err := node.DialWorkspace(ctx, workspace, port)
	if err != nil {
		return fmt.Errorf("mesh dial %s: %w", workspace, err)
	}
	defer conn.Close()

	done := make(chan error, 2)
	go func() { done <- copyConnToStdout(conn) }()
	go func() { done <- copyStdinToConn(conn) }()
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

func copyConnToStdout(conn net.Conn) error {
	_, err := io.Copy(os.Stdout, conn)
	return err
}

func copyStdinToConn(conn net.Conn) error {
	_, err := io.Copy(conn, os.Stdin)
	return err
}

func isExpectedClose(err error) bool {
	return websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway, websocket.CloseNoStatusReceived)
}
