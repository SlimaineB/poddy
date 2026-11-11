package bastion

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	corev1 "k8s.io/api/core/v1"

	"poddy/internal/k8s"
	"poddy/internal/metadata"
	"poddy/internal/sshca"
	"poddy/internal/sshkeys"
	"poddy/internal/tunnel"
)

type Bastion struct {
	Addr         string
	Manager      *k8s.Manager
	Audit        metadata.Auditor
	Broker       *tunnel.Broker
	RequireAgent bool
	CA           *sshca.CA
	Access       *k8s.AccessStore
	keys         sshkeys.Keyring
	config       *ssh.ServerConfig
	listener     net.Listener
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

	b := &Bastion{Addr: addr, Manager: manager, keys: keys}
	b.config = &ssh.ServerConfig{PublicKeyCallback: b.publicKeyCallback}
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
		go b.handle(conn)
	}
}

func (b *Bastion) Close() error {
	var err error
	if b.listener != nil {
		err = b.listener.Close()
	}
	if b.Broker != nil {
		b.Broker.Close()
	}
	return err
}

func (b *Bastion) handle(conn net.Conn) {
	defer conn.Close()

	serverConn, channels, requests, err := ssh.NewServerConn(conn, b.config)
	if err != nil {
		log.Printf("ssh bastion: handshake: %v", err)
		return
	}
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

func (b *Bastion) dialWorkspace(ctx context.Context, name string, port int) (net.Conn, func(), string, error) {
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
		log.Printf("ssh bastion: audit %s: %v", workspace, err)
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

	if data, err := os.ReadFile(path); err == nil {
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
