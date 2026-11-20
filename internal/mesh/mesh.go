// Package mesh joins poddy components to a WireGuard tailnet (Headscale or
// Tailscale). tsnet handles WireGuard, NAT traversal and DERP fallback so the
// data plane never depends on a central SSH bastion.
package mesh

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tsnet"
)

const maxHostname = 63

type Config struct {
	ControlURL string
	AuthKey    string
	Hostname   string
	StateDir   string
	Ephemeral  bool
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
	dir := strings.TrimSpace(cfg.StateDir)
	if dir == "" {
		dir = "/tmp/poddy-mesh/" + hostname
	}
	srv := &tsnet.Server{
		Hostname:   hostname,
		AuthKey:    cfg.AuthKey,
		ControlURL: strings.TrimSpace(cfg.ControlURL),
		Dir:        dir,
		Ephemeral:  cfg.Ephemeral,
	}
	if _, err := srv.Up(ctx); err != nil {
		_ = srv.Close()
		return nil, fmt.Errorf("mesh: join tailnet: %w", err)
	}
	return &Node{srv: srv, peers: map[string]string{}}, nil
}

func (n *Node) Close() error {
	if n == nil || n.srv == nil {
		return nil
	}
	return n.srv.Close()
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
	return n.DialHost(ctx, WorkspaceHostname(workspace), port)
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
