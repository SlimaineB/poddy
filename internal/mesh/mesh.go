// Package mesh joins poddy components to a WireGuard tailnet (Headscale or
// Tailscale). tsnet handles WireGuard, NAT traversal and DERP fallback so the
// data plane never depends on a central SSH bastion.
package mesh

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tsnet"
)

const (
	maxHostname        = 63
	joinAttemptTimeout = 45 * time.Second
)

type Config struct {
	ControlURL string
	AuthKey    string
	Hostname   string
	StateDir   string
	Ephemeral  bool
	Silent     bool
	RandomPort bool
	CACert     string
}

type Node struct {
	srv *tsnet.Server

	mu       sync.Mutex
	peers    map[string]string
	cachedAt time.Time
}

func Start(ctx context.Context, cfg Config) (*Node, error) {
	hostname := SanitizeHostname(cfg.Hostname)
	if hostname == "" {
		return nil, fmt.Errorf("mesh: hostname is required")
	}
	if strings.TrimSpace(cfg.AuthKey) == "" {
		return nil, fmt.Errorf("mesh: auth key is required")
	}
	if strings.TrimSpace(cfg.CACert) != "" {
		if err := installCARoots([]byte(cfg.CACert)); err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(cfg.ControlURL) != "" {
		preferIPv4Control(cfg.ControlURL)
	}
	dir := strings.TrimSpace(cfg.StateDir)
	if dir == "" {
		dir = "/tmp/poddy-mesh/" + hostname
	}
	forceLoginForNewState(dir)
	port := uint16(41641)
	if cfg.RandomPort {
		port = 0
	}
	newServer := func() *tsnet.Server {
		srv := &tsnet.Server{
			Hostname:   hostname,
			AuthKey:    cfg.AuthKey,
			ControlURL: strings.TrimSpace(cfg.ControlURL),
			Dir:        dir,
			Ephemeral:  cfg.Ephemeral,
			Port:       port,
		}
		if cfg.Silent {
			srv.Logf = func(string, ...any) {}
			srv.UserLogf = func(string, ...any) {}
		} else if os.Getenv("PODDY_MESH_DEBUG") != "" {
			srv.Logf = log.Printf
			srv.UserLogf = log.Printf
		}
		return srv
	}
	up := func(srv *tsnet.Server) error {
		upCtx, cancel := context.WithTimeout(ctx, joinAttemptTimeout)
		defer cancel()
		if _, err := srv.Up(upCtx); err != nil {
			if errors.Is(upCtx.Err(), context.DeadlineExceeded) {
				return fmt.Errorf("timed out after %s: %w", joinAttemptTimeout, err)
			}
			return err
		}
		return nil
	}

	srv := newServer()
	if err := up(srv); err != nil {
		_ = srv.Close()
		if ctx.Err() != nil {
			return nil, fmt.Errorf("mesh: join tailnet: %w", err)
		}
		// A state file can survive a crash while the backend is still in
		// NoState. In that case tsnet ignores the auth key and Up blocks
		// forever. Retry once with an explicit login, which re-enrolls the
		// node instead of leaving the agent hung.
		log.Printf("mesh: first join attempt failed (%v); retrying with forced login", err)
		os.Setenv("TSNET_FORCE_LOGIN", "1")
		srv = newServer()
		if err := up(srv); err != nil {
			_ = srv.Close()
			return nil, fmt.Errorf("mesh: join tailnet: %w", err)
		}
	}
	return &Node{srv: srv, peers: map[string]string{}}, nil
}

// forceLoginForNewState makes tsnet enroll with the provided auth key on a
// fresh state directory. Without this, a first run stays in NoState, ignores
// the key and waits for an interactive login.
func forceLoginForNewState(dir string) {
	state, err := os.Stat(filepath.Join(dir, "tailscaled.state"))
	if err == nil && state.Size() > 0 {
		return
	}
	if os.Getenv("TSNET_FORCE_LOGIN") == "" {
		os.Setenv("TSNET_FORCE_LOGIN", "1")
	}
}

func (n *Node) Close() error {
	if n == nil || n.srv == nil {
		return nil
	}
	return n.srv.Close()
}

// Ready reports whether the local WireGuard backend is actually running and
// still connected to the control plane. A process can be alive, report a
// Running backend, and still be unable to reach the tailnet after a control
// plane restart, so readiness must check the map poll state as well.
func (n *Node) Ready(ctx context.Context) error {
	if n == nil || n.srv == nil {
		return fmt.Errorf("mesh: node is not running")
	}
	client, err := n.srv.LocalClient()
	if err != nil {
		return fmt.Errorf("mesh: local client: %w", err)
	}
	status, err := client.Status(ctx)
	if err != nil {
		return fmt.Errorf("mesh: status: %w", err)
	}
	return readyStatusError(status)
}

func (n *Node) WorkspaceOnline(ctx context.Context, workspace string) (bool, error) {
	if n == nil || n.srv == nil {
		return false, fmt.Errorf("mesh: node is not running")
	}
	_, online, found, err := n.peerState(ctx, WorkspaceHostname(workspace))
	if err != nil {
		return false, err
	}
	return found && online, nil
}

func readyStatusError(status *ipnstate.Status) error {
	if status == nil {
		return fmt.Errorf("mesh: status is unavailable")
	}
	if status.BackendState != "Running" {
		state := strings.TrimSpace(status.BackendState)
		if state == "" {
			state = "unknown"
		}
		return fmt.Errorf("mesh: backend state is %s", state)
	}
	if status.Self == nil || !status.Self.Online {
		return fmt.Errorf("mesh: control plane connection is not active")
	}
	return nil
}

func (n *Node) Listen(network, addr string) (net.Listener, error) {
	if n == nil {
		return nil, fmt.Errorf("mesh: node is not running")
	}
	return n.srv.Listen(network, addr)
}

func (n *Node) DialHost(ctx context.Context, host string, port int) (net.Conn, error) {
	if n == nil {
		return nil, fmt.Errorf("mesh: node is not running")
	}
	host = strings.TrimSuffix(strings.TrimSpace(host), ".")
	if host == "" {
		return nil, fmt.Errorf("mesh: host is required")
	}
	addr := net.JoinHostPort(host, fmt.Sprint(port))
	conn, err := n.srv.Dial(ctx, "tcp", addr)
	if err == nil {
		return conn, nil
	}
	ip, resolveErr := n.resolve(ctx, host)
	if resolveErr != nil {
		return nil, err
	}
	return n.srv.Dial(ctx, "tcp", net.JoinHostPort(ip, fmt.Sprint(port)))
}

func (n *Node) DialWorkspace(ctx context.Context, workspace string, port int) (net.Conn, error) {
	host := WorkspaceHostname(workspace)
	deadline := time.Now().Add(45 * time.Second)
	var lastErr error
	var lastIP string
	var lastState string
	for {
		ip, online, found, err := n.peerState(ctx, host)
		switch {
		case err != nil:
			lastErr = err
			lastState = "status unavailable"
		case found && !online:
			lastIP = ip
			lastErr = fmt.Errorf("mesh: workspace peer %q is offline", host)
			lastState = "peer offline"
		case found:
			lastIP = ip
			lastState = "peer online"
			dialCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			conn, dialErr := n.srv.Dial(dialCtx, "tcp", net.JoinHostPort(ip, fmt.Sprint(port)))
			cancel()
			if dialErr == nil {
				return conn, nil
			}
			lastErr = dialErr
		default:
			lastErr = fmt.Errorf("mesh: workspace peer %q not found", host)
			lastState = "peer not found"
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if time.Now().After(deadline) {
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
	detail := lastState
	if detail == "" {
		detail = "no peer state"
	}
	if lastIP != "" {
		detail = fmt.Sprintf("%s at %s", detail, net.JoinHostPort(lastIP, fmt.Sprint(port)))
	}
	return nil, fmt.Errorf("mesh: dial workspace %q (%s): %w", workspace, detail, lastErr)
}

func (n *Node) peerState(ctx context.Context, prefix string) (ip string, online bool, found bool, err error) {
	client, err := n.srv.LocalClient()
	if err != nil {
		return "", false, false, err
	}
	status, err := client.Status(ctx)
	if err != nil {
		return "", false, false, err
	}
	peer := selectPeer(status, prefix)
	if peer == nil {
		return "", false, false, nil
	}
	return peerIP(peer), peer.Online, true, nil
}

// selectPeer returns the best peer matching prefix. An exact hostname match is
// preferred over a suffixed one, an online node over a stale offline duplicate,
// a node with a direct path over a relay-only one, and finally the most recently
// seen node. Headscale can keep several nodes with the same hostname after a
// workspace restarts, so picking the first map entry is not reliable.
func selectPeer(status *ipnstate.Status, prefix string) *ipnstate.PeerStatus {
	if status == nil {
		return nil
	}
	var exact, prefixed *ipnstate.PeerStatus
	for _, peer := range status.Peer {
		if peer == nil {
			continue
		}
		for _, name := range []string{peer.HostName, strings.TrimSuffix(peer.DNSName, ".")} {
			name = strings.ToLower(name)
			switch {
			case name == prefix:
				exact = betterPeer(exact, peer)
			case strings.HasPrefix(name, prefix+"-") || strings.HasPrefix(name, prefix+"."):
				prefixed = betterPeer(prefixed, peer)
			}
		}
	}
	if exact != nil {
		return exact
	}
	return prefixed
}

func betterPeer(current, candidate *ipnstate.PeerStatus) *ipnstate.PeerStatus {
	if current == nil {
		return candidate
	}
	if candidate.Online != current.Online {
		if candidate.Online {
			return candidate
		}
		return current
	}
	if (candidate.CurAddr != "") != (current.CurAddr != "") {
		if candidate.CurAddr != "" {
			return candidate
		}
		return current
	}
	if candidate.LastSeen.After(current.LastSeen) {
		return candidate
	}
	return current
}

func (n *Node) findPeerIP(ctx context.Context, prefix string) (string, error) {
	client, err := n.srv.LocalClient()
	if err != nil {
		return "", err
	}
	status, err := client.Status(ctx)
	if err != nil {
		return "", err
	}
	peer := selectPeer(status, prefix)
	if peer == nil || !peer.Online {
		return "", fmt.Errorf("mesh: workspace peer %q not found", prefix)
	}
	if ip := peerIP(peer); ip != "" {
		return ip, nil
	}
	return "", fmt.Errorf("mesh: workspace peer %q has no IP", prefix)
}

func (n *Node) DialRegion(ctx context.Context, regionName string, port int) (net.Conn, error) {
	return n.DialHost(ctx, RegionHostname(regionName), port)
}

func (n *Node) HTTPClient() *http.Client {
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, portText, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			port, err := net.LookupPort("tcp", portText)
			if err != nil {
				return nil, err
			}
			return n.DialHost(ctx, host, port)
		},
		IdleConnTimeout:     30 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	}
	return &http.Client{Transport: transport}
}

func (n *Node) resolve(ctx context.Context, host string) (string, error) {
	host = strings.ToLower(host)
	n.mu.Lock()
	if ip, ok := n.peers[host]; ok && time.Since(n.cachedAt) < 5*time.Second {
		n.mu.Unlock()
		return ip, nil
	}
	n.mu.Unlock()

	client, err := n.srv.LocalClient()
	if err != nil {
		return "", err
	}
	status, err := client.Status(ctx)
	if err != nil {
		return "", err
	}
	peers := map[string]string{}
	if status != nil {
		for _, peer := range status.Peer {
			if ip := peerIP(peer); ip != "" {
				peers[strings.ToLower(strings.TrimSuffix(peer.DNSName, "."))] = ip
				peers[strings.ToLower(peer.HostName)] = ip
			}
		}
	}
	n.mu.Lock()
	n.peers = peers
	n.cachedAt = time.Now()
	ip, ok := peers[host]
	n.mu.Unlock()
	if !ok {
		return "", fmt.Errorf("mesh: peer %q not found", host)
	}
	return ip, nil
}

func peerIP(peer *ipnstate.PeerStatus) string {
	if peer == nil {
		return ""
	}
	for _, addr := range peer.TailscaleIPs {
		if addr.Is4() {
			return addr.String()
		}
	}
	for _, addr := range peer.TailscaleIPs {
		if addr.IsValid() {
			return addr.String()
		}
	}
	return ""
}

func WorkspaceHostname(workspace string) string {
	return prefixedHostname("ws-", workspace)
}

func RegionHostname(regionName string) string {
	return prefixedHostname("reg-", regionName)
}

func PortalHostname(base string) string {
	return prefixedHostname("poddy-portal-", base)
}

func prefixedHostname(prefix, value string) string {
	name := SanitizeHostname(value)
	if name == "" {
		name = "unnamed"
	}
	full := prefix + name
	if len(full) <= maxHostname {
		return full
	}
	sum := sha256.Sum256([]byte(full))
	suffix := "-" + hex.EncodeToString(sum[:3])
	keep := maxHostname - len(prefix) - len(suffix)
	if keep < 1 {
		keep = 1
	}
	return prefix + name[:keep] + suffix
}

func SanitizeHostname(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > maxHostname {
		out = out[:maxHostname]
		out = strings.Trim(out, "-")
	}
	return out
}

func installCARoots(pem []byte) error {
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem) {
		return fmt.Errorf("mesh: CA bundle contains no certificates")
	}
	godebug := strings.TrimSpace(os.Getenv("GODEBUG"))
	if godebug != "" {
		godebug += ","
	}
	_ = os.Setenv("GODEBUG", godebug+"x509usefallbackroots=1")
	x509.SetFallbackRoots(roots)
	return nil
}
