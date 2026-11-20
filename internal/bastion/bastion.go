package bastion

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/ssh"
	corev1 "k8s.io/api/core/v1"

	"poddy/internal/k8s"
	"poddy/internal/metadata"
	"poddy/internal/sshca"
	"poddy/internal/sshkeys"
	"poddy/internal/tunnel"
)

const (
	defaultMaxConnections      = 512
	defaultMaxConnectionsPerIP = 32
	defaultDrainDelay          = 3 * time.Second
	handshakeTimeout           = 15 * time.Second
	sshServerVersion           = "SSH-2.0-Poddy"
)

type RemoteDialer interface {
	DialWorkspace(ctx context.Context, workspace string, port int) (net.Conn, error)
	DialRegionWorkspace(ctx context.Context, workspace string, port int) (net.Conn, error)
}

type Bastion struct {
	Addr         string
	Manager      *k8s.Manager
	Audit        metadata.Auditor
	Broker       *tunnel.Broker
	Remote       RemoteDialer
	RegionBroker *tunnel.RegionBroker
	// RegionOwner resolves the owner of a workspace that lives in a region.
	// It lets a self-service SSH key use the same per-workspace authorization
	// as certificates when the hub only knows the workspace through a region.
	RegionOwner  func(context.Context, string) (string, error)
	InternalKey  []byte
	RequireAgent bool
	CA           *sshca.CA
	Access       k8s.AccessStore
	keys         sshkeys.Keyring
	config       *ssh.ServerConfig
	listener     net.Listener
	AgentServer  *http.Server
	DrainDelay   time.Duration

	MaxConnections      int
	MaxConnectionsPerIP int

	connMu   sync.Mutex
	conns    map[net.Conn]struct{}
	draining atomic.Bool

	limitMu sync.Mutex
	active  int
	perIP   map[string]int
}

type directTCPIP struct {
	Host       string
	Port       uint32
	OriginHost string
	OriginPort uint32
}

func New(addr string, manager *k8s.Manager, keys sshkeys.Keyring, hostKeyPath string) (*Bastion, error) {
	if keys == nil {
		return nil, fmt.Errorf("ssh keyring is required")
	}
	signer, err := loadHostKey(hostKeyPath)
	if err != nil {
		return nil, err
	}

	b := &Bastion{
		Addr:                addr,
		Manager:             manager,
		keys:                keys,
		MaxConnections:      defaultMaxConnections,
		MaxConnectionsPerIP: defaultMaxConnectionsPerIP,
		DrainDelay:          defaultDrainDelay,
		perIP:               map[string]int{},
		conns:               map[net.Conn]struct{}{},
	}
	b.config = &ssh.ServerConfig{
		MaxAuthTries:      3,
		ServerVersion:     sshServerVersion,
		PublicKeyCallback: b.publicKeyCallback,
		Config: ssh.Config{
			KeyExchanges: []string{
				"curve25519-sha256",
				"curve25519-sha256@libssh.org",
				"ecdh-sha2-nistp256",
				"ecdh-sha2-nistp384",
				"ecdh-sha2-nistp521",
			},
			Ciphers: []string{
				"chacha20-poly1305@openssh.com",
				"aes256-gcm@openssh.com",
				"aes128-gcm@openssh.com",
			},
			MACs: []string{
				"hmac-sha2-256-etm@openssh.com",
				"hmac-sha2-512-etm@openssh.com",
				"hmac-sha2-256",
				"hmac-sha2-512",
			},
		},
		PublicKeyAuthAlgorithms: []string{
			ssh.KeyAlgoED25519,
			ssh.KeyAlgoECDSA256,
			ssh.KeyAlgoECDSA384,
			ssh.KeyAlgoECDSA521,
			ssh.KeyAlgoRSASHA256,
			ssh.KeyAlgoRSASHA512,
		},
	}
	b.config.AddHostKey(signer)
	return b, nil
}

func (b *Bastion) publicKeyCallback(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
	if cert, ok := key.(*ssh.Certificate); ok {
		if b.CA == nil {
			return nil, fmt.Errorf("SSH certificate authentication is not configured")
		}
		if err := b.CA.VerifyCert(cert); err != nil {
			return nil, fmt.Errorf("invalid SSH certificate: %w", err)
		}
		if len(cert.ValidPrincipals) != 1 || cert.ValidPrincipals[0] != sshca.WorkspacePrincipal {
			return nil, fmt.Errorf("invalid certificate principal")
		}
		owner := strings.TrimSpace(cert.KeyId)
		if owner == "" {
			return nil, fmt.Errorf("certificate has no owner")
		}
		extensions := map[string]string{
			"actor":       owner,
			"actorType":   "user",
			"owner":       owner,
			"fingerprint": ssh.FingerprintSHA256(cert.Key),
		}
		if workspace := sshca.Workspace(cert); workspace != "" {
			extensions["workspace"] = workspace
		}
		if sshca.Admin(cert) {
			extensions["admin"] = "true"
		}
		extensions["certificate"] = base64.StdEncoding.EncodeToString(cert.Marshal())
		return &ssh.Permissions{Extensions: extensions}, nil
	}

	fingerprint := ssh.FingerprintSHA256(key)
	if _, ok := b.keys.Lookup(fingerprint); !ok {
		return nil, fmt.Errorf("unknown public key")
	}
	return &ssh.Permissions{Extensions: map[string]string{"fingerprint": fingerprint}}, nil
}

// Listen binds the bastion address. Serve reuses an existing listener.
func (b *Bastion) Listen() error {
	if b.listener != nil {
		return nil
	}
	listener, err := net.Listen("tcp", b.Addr)
	if err != nil {
		return err
	}
	b.listener = listener
	return nil
}

func (b *Bastion) Serve() error {
	if err := b.Listen(); err != nil {
		return err
	}

	for {
		conn, err := b.listener.Accept()
		if err != nil {
			return err
		}
		if b.Draining() {
			_ = conn.Close()
			continue
		}
		ip := remoteIP(conn.RemoteAddr().String())
		if !b.acquire(ip) {
			_ = conn.Close()
			continue
		}
		b.trackConn(conn)
		go func() {
			defer b.release(ip)
			defer b.untrackConn(conn)
			b.handle(conn)
		}()
	}
}

func (b *Bastion) trackConn(conn net.Conn) {
	b.connMu.Lock()
	if b.conns == nil {
		b.conns = map[net.Conn]struct{}{}
	}
	b.conns[conn] = struct{}{}
	b.connMu.Unlock()
}

func (b *Bastion) untrackConn(conn net.Conn) {
	b.connMu.Lock()
	delete(b.conns, conn)
	b.connMu.Unlock()
}

func (b *Bastion) Draining() bool {
	return b != nil && b.draining.Load()
}

func (b *Bastion) closeConnections() {
	b.connMu.Lock()
	conns := make([]net.Conn, 0, len(b.conns))
	for conn := range b.conns {
		conns = append(conns, conn)
	}
	b.connMu.Unlock()
	for _, conn := range conns {
		_ = conn.Close()
	}
}

func (b *Bastion) activeConnections() int {
	b.limitMu.Lock()
	defer b.limitMu.Unlock()
	return b.active
}

// Drain rejects new SSH connections and agent tunnels immediately, then keeps
// existing SSH sessions alive until they finish or ctx expires. A short delay
// before closing the listener lets Kubernetes remove the pod endpoint.
func (b *Bastion) Drain(ctx context.Context) error {
	if b == nil {
		return nil
	}
	b.draining.Store(true)
	if b.DrainDelay > 0 {
		timer := time.NewTimer(b.DrainDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			b.closeConnections()
			return ctx.Err()
		case <-timer.C:
		}
	}
	if b.listener != nil {
		_ = b.listener.Close()
	}
	if b.AgentServer != nil {
		_ = b.AgentServer.Shutdown(ctx)
	}

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if b.activeConnections() == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			b.closeConnections()
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (b *Bastion) acquire(ip string) bool {
	b.limitMu.Lock()
	defer b.limitMu.Unlock()
	max := b.MaxConnections
	if max <= 0 {
		max = defaultMaxConnections
	}
	perIPMax := b.MaxConnectionsPerIP
	if perIPMax <= 0 {
		perIPMax = defaultMaxConnectionsPerIP
	}
	if b.active >= max || b.perIP[ip] >= perIPMax {
		return false
	}
	b.active++
	b.perIP[ip]++
	return true
}

func (b *Bastion) release(ip string) {
	b.limitMu.Lock()
	defer b.limitMu.Unlock()
	if b.active > 0 {
		b.active--
	}
	if b.perIP[ip] <= 1 {
		delete(b.perIP, ip)
		return
	}
	b.perIP[ip]--
}

func (b *Bastion) Close() error {
	var err error
	if b.listener != nil {
		err = b.listener.Close()
	}
	b.closeConnections()
	if b.Broker != nil {
		b.Broker.Close()
	}
	if b.AgentServer != nil {
		_ = b.AgentServer.Close()
	}
	return err
}

func (b *Bastion) handle(conn net.Conn) {
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(handshakeTimeout))
	serverConn, channels, requests, err := ssh.NewServerConn(conn, b.config)
	if err != nil {
		log.Printf("ssh bastion: handshake: %v", err)
		return
	}
	_ = conn.SetDeadline(time.Time{})
	defer serverConn.Close()
	go ssh.DiscardRequests(requests)

	for newChannel := range channels {
		if newChannel.ChannelType() != "direct-tcpip" {
			_ = newChannel.Reject(ssh.UnknownChannelType, "only direct-tcpip is supported")
			continue
		}
		go b.forward(newChannel, serverConn.Permissions, conn.RemoteAddr().String())
	}
}

func (b *Bastion) forward(newChannel ssh.NewChannel, permissions *ssh.Permissions, remoteAddr string) {
	var request directTCPIP
	if err := ssh.Unmarshal(newChannel.ExtraData(), &request); err != nil {
		_ = newChannel.Reject(ssh.ConnectionFailed, "invalid direct-tcpip request")
		return
	}

	name := workspaceName(request.Host)
	fingerprint := ""
	actor := ""
	actorType := "user"
	if permissions != nil {
		fingerprint = permissions.Extensions["fingerprint"]
		actor = strings.TrimSpace(permissions.Extensions["actor"])
		if permissions.Extensions["actorType"] != "" {
			actorType = permissions.Extensions["actorType"]
		}
	}
	if actor == "" {
		actor, actorType = b.auditActor(fingerprint)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pod, err := b.Manager.Get(ctx, name)
	if err != nil {
		port := int(request.Port)
		if port == 0 {
			port = 22
		}
		if b.regionAuthorized(ctx, permissions, fingerprint, name) {
			if port != 22 {
				b.recordAudit(name, actor, actorType, remoteAddr, "denied", "only the workspace SSH port is allowed")
				_ = newChannel.Reject(ssh.Prohibited, "only the workspace SSH port is allowed")
				return
			}
			target, dialErr := b.Remote.DialRegionWorkspace(ctx, name, port)
			if dialErr != nil {
				log.Printf("ssh bastion: peer dial %s: %v", name, dialErr)
				b.recordAudit(name, actor, actorType, remoteAddr, "error", dialErr.Error())
				_ = newChannel.Reject(ssh.ConnectionFailed, "cannot reach workspace")
				return
			}
			b.serveChannel(newChannel, name, actor, actorType, remoteAddr, target, "tunnel")
			return
		}
		b.recordAudit(name, actor, actorType, remoteAddr, "error", "workspace not found")
		_ = newChannel.Reject(ssh.ConnectionFailed, "workspace not found")
		return
	}
	if pod.Status.Phase != corev1.PodRunning {
		b.recordAudit(name, actor, actorType, remoteAddr, "error", "workspace is not running")
		_ = newChannel.Reject(ssh.ConnectionFailed, "workspace is not running")
		return
	}

	if !b.authorize(permissions, fingerprint, k8s.Owner(pod), name) {
		log.Printf("ssh bastion: key %s is not authorized for workspace %s", fingerprint, name)
		b.recordAudit(name, actor, actorType, remoteAddr, "denied", "not authorized for this workspace")
		_ = newChannel.Reject(ssh.Prohibited, "not authorized for this workspace")
		return
	}

	if !b.interactiveAuthorized(permissions, actor, name, k8s.Owner(pod)) {
		log.Printf("ssh bastion: admin %s is waiting for owner approval on workspace %s", actor, name)
		b.recordAudit(name, actor, actorType, remoteAddr, "denied", "owner approval required")
		_ = newChannel.Reject(ssh.Prohibited, "owner approval required: request access from the portal")
		return
	}

	port := int(request.Port)
	if port == 0 {
		port = 22
	}
	if port != 22 {
		b.recordAudit(name, actor, actorType, remoteAddr, "denied", "only the workspace SSH port is allowed")
		_ = newChannel.Reject(ssh.Prohibited, "only the workspace SSH port is allowed")
		return
	}

	target, stop, transport, err := b.dialWorkspace(ctx, name, port)
	if err != nil {
		log.Printf("ssh bastion: dial %s: %v", name, err)
		b.recordAudit(name, actor, actorType, remoteAddr, "error", err.Error())
		_ = newChannel.Reject(ssh.ConnectionFailed, "cannot reach workspace")
		return
	}
	if stop != nil {
		defer stop()
	}
	b.serveChannel(newChannel, name, actor, actorType, remoteAddr, target, transport)
}

func (b *Bastion) serveChannel(newChannel ssh.NewChannel, name, actor, actorType, remoteAddr string, target net.Conn, transport string) {
	defer target.Close()

	channel, channelRequests, err := newChannel.Accept()
	if err != nil {
		b.recordAudit(name, actor, actorType, remoteAddr, "error", err.Error())
		return
	}
	b.recordAudit(name, actor, actorType, remoteAddr, "success", "transport="+transport)
	go ssh.DiscardRequests(channelRequests)
	stopPresence := b.trackPresence(name, actor)
	defer stopPresence()

	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(channel, target)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(target, channel)
		done <- struct{}{}
	}()

	<-done
	_ = channel.Close()
}

// regionAuthorized allows a scoped certificate or a self-service key owned by
// the region workspace owner to reach a workspace through the region tunnel.
func (b *Bastion) regionAuthorized(ctx context.Context, permissions *ssh.Permissions, fingerprint, workspace string) bool {
	if b.Remote == nil {
		return false
	}
	if b.scopedCertAuthorized(permissions, workspace) {
		return true
	}
	if b.RegionOwner == nil || fingerprint == "" {
		return false
	}
	owner, err := b.RegionOwner(ctx, workspace)
	if err != nil {
		return false
	}
	return b.keys.Authorize(fingerprint, owner)
}

func (b *Bastion) scopedCertAuthorized(permissions *ssh.Permissions, workspace string) bool {
	if permissions == nil {
		return false
	}
	ext := permissions.Extensions
	if strings.TrimSpace(ext["certificate"]) == "" || strings.TrimSpace(ext["actor"]) == "" {
		return false
	}
	scope := strings.TrimSpace(ext["workspace"])
	return scope != "" && strings.EqualFold(scope, workspace)
}

func (b *Bastion) dialWorkspace(ctx context.Context, name string, port int) (net.Conn, func(), string, error) {
	if b.Remote != nil {
		if conn, err := b.Remote.DialWorkspace(ctx, name, port); err == nil {
			return conn, nil, "remote", nil
		}
	}
	if b.Broker != nil && b.Broker.Connected(name) {
		conn, err := b.Broker.Dial(ctx, name, fmt.Sprintf("127.0.0.1:%d", port))
		if err == nil {
			return conn, nil, "agent", nil
		}
		if b.RequireAgent {
			return nil, nil, "", fmt.Errorf("workspace agent: %w", err)
		}
		log.Printf("ssh bastion: agent dial %s failed, falling back to port-forward: %v", name, err)
	}
	if b.RequireAgent {
		return nil, nil, "", fmt.Errorf("workspace agent is not connected")
	}
	local, stop, err := b.Manager.PortForward(k8s.PodName(name), port)
	if err != nil {
		return nil, nil, "", fmt.Errorf("port-forward failed: %w", err)
	}
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", local), 5*time.Second)
	if err != nil {
		stop()
		return nil, nil, "", fmt.Errorf("dial workspace failed: %w", err)
	}
	return conn, stop, "port-forward", nil
}

func (b *Bastion) authorize(permissions *ssh.Permissions, fingerprint, owner, workspace string) bool {
	if permissions != nil {
		if certOwner := strings.TrimSpace(permissions.Extensions["actor"]); certOwner != "" {
			if permissions.Extensions["admin"] == "true" {
				return true
			}
			if !strings.EqualFold(certOwner, owner) {
				return false
			}
			if scope := strings.TrimSpace(permissions.Extensions["workspace"]); scope != "" && !strings.EqualFold(scope, workspace) {
				return false
			}
			return true
		}
	}
	return b.keys.Authorize(fingerprint, owner)
}

func (b *Bastion) interactiveAuthorized(permissions *ssh.Permissions, actor, workspace, owner string) bool {
	if permissions == nil || permissions.Extensions["admin"] != "true" {
		return true
	}
	actor = strings.TrimSpace(actor)
	if actor == "" || owner == "" || strings.EqualFold(actor, owner) {
		return true
	}
	if b.Access == nil {
		return true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	state, err := b.Access.Load(ctx)
	if err != nil {
		log.Printf("ssh bastion: access check %s: %v", workspace, err)
		return false
	}
	_, ok := state.Grant(workspace, actor, time.Now())
	return ok
}

func (b *Bastion) trackPresence(workspace, actor string) func() {
	actor = strings.TrimSpace(actor)
	if b.Access == nil || actor == "" {
		return func() {}
	}
	session, err := k8s.NewPresenceID()
	if err != nil {
		return func() {}
	}
	now := time.Now()
	presence := k8s.Presence{Actor: actor, Kind: "ssh", Since: now, LastSeen: now}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := b.Access.SetPresence(ctx, workspace, session, presence); err != nil {
		log.Printf("ssh bastion: presence %s/%s: %v", workspace, actor, err)
	}
	cancel()

	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				presence.LastSeen = time.Now()
				hbCtx, hbCancel := context.WithTimeout(context.Background(), 5*time.Second)
				err := b.Access.SetPresence(hbCtx, workspace, session, presence)
				hbCancel()
				if err != nil {
					log.Printf("ssh bastion: presence heartbeat %s/%s: %v", workspace, actor, err)
				}
			}
		}
	}()
	return func() {
		close(done)
		clearCtx, clearCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer clearCancel()
		if err := b.Access.ClearPresence(clearCtx, workspace, session); err != nil {
			log.Printf("ssh bastion: presence clear %s/%s: %v", workspace, actor, err)
		}
	}
}

func (b *Bastion) auditActor(fingerprint string) (string, string) {
	if key, ok := b.keys.Lookup(fingerprint); ok {
		if key.User != "" {
			return key.User, "user"
		}
		if key.Admin {
			return "admin", "user"
		}
	}
	if fingerprint != "" {
		return fingerprint, "user"
	}
	return "", "user"
}

func (b *Bastion) recordAudit(workspace, actor, actorType, sourceIP, outcome, message string) {
	b.recordEvent(workspace, actor, actorType, sourceIP, "ssh.connect", outcome, message)
}

func (b *Bastion) recordEvent(workspace, actor, actorType, sourceIP, action, outcome, message string) {
	if b.Audit == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := b.Audit.RecordAudit(ctx, metadata.Event{
		Namespace:  b.Manager.Namespace,
		Actor:      actor,
		ActorType:  actorType,
		Action:     action,
		Outcome:    outcome,
		TargetType: "workspace",
		TargetName: workspace,
		Workspace:  workspace,
		SourceIP:   sourceIP,
		Error:      message,
	}); err != nil {
		log.Printf("ssh bastion: audit %s: %v", strings.ReplaceAll(strings.ReplaceAll(workspace, "\n", " "), "\r", " "), strings.ReplaceAll(strings.ReplaceAll(err.Error(), "\n", " "), "\r", " "))
	}
}

func workspaceName(host string) string {
	host = strings.TrimSpace(host)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.TrimPrefix(host, "[")
	host = strings.TrimSuffix(host, "]")
	name := strings.TrimPrefix(host, "poddy-ssh-")
	if name == host {
		name = strings.TrimPrefix(host, "poddy-")
	}
	return name
}

func loadHostKey(path string) (ssh.Signer, error) {
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			path = filepath.Join(os.TempDir(), "poddy_ssh_host_ed25519_key")
		} else {
			path = filepath.Join(home, ".poddy", "ssh_host_ed25519_key")
		}
	}

	if data, err := os.ReadFile(path); err == nil { // #nosec G304 -- Path is supplied by trusted configuration.
		return ssh.ParsePrivateKey(data)
	}

	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	block, err := ssh.MarshalPrivateKey(private, "poddy")
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		log.Printf("ssh bastion: host key directory: %v", err)
	} else if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		log.Printf("ssh bastion: persist host key: %v", err)
	}
	return ssh.NewSignerFromKey(private)
}
