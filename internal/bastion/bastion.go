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
	"poddy/internal/sshkeys"
)

type Bastion struct {
	Addr     string
	Manager  *k8s.Manager
	Audit    metadata.Auditor
	keys     sshkeys.Keyring
	config   *ssh.ServerConfig
	listener net.Listener
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

	config := &ssh.ServerConfig{
		PublicKeyCallback: func(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			fingerprint := ssh.FingerprintSHA256(key)
			if _, ok := keys.Lookup(fingerprint); !ok {
				return nil, fmt.Errorf("unknown public key")
			}
			return &ssh.Permissions{Extensions: map[string]string{"fingerprint": fingerprint}}, nil
		},
	}
	config.AddHostKey(signer)

	return &Bastion{Addr: addr, Manager: manager, keys: keys, config: config}, nil
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
	if b.listener == nil {
		return nil
	}
	return b.listener.Close()
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
	if permissions != nil {
		fingerprint = permissions.Extensions["fingerprint"]
	}
	actor, actorType := b.auditActor(fingerprint)
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

	if !b.keys.Authorize(fingerprint, k8s.Owner(pod)) {
		log.Printf("ssh bastion: key %s is not authorized for workspace %s", fingerprint, name)
		b.recordAudit(name, actor, actorType, remoteAddr, "denied", "not authorized for this workspace")
		_ = newChannel.Reject(ssh.Prohibited, "not authorized for this workspace")
		return
	}

	port := int(request.Port)
	if port == 0 {
		port = 22
	}

	local, stop, err := b.Manager.PortForward(k8s.PodName(name), port)
	if err != nil {
		log.Printf("ssh bastion: port-forward %s: %v", name, err)
		b.recordAudit(name, actor, actorType, remoteAddr, "error", "port-forward failed: "+err.Error())
		_ = newChannel.Reject(ssh.ConnectionFailed, "cannot reach workspace")
		return
	}
	defer stop()

	target, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", local), 5*time.Second)
	if err != nil {
		log.Printf("ssh bastion: dial %s: %v", name, err)
		b.recordAudit(name, actor, actorType, remoteAddr, "error", "dial workspace failed: "+err.Error())
		_ = newChannel.Reject(ssh.ConnectionFailed, "cannot reach workspace")
		return
	}
	defer target.Close()

	channel, channelRequests, err := newChannel.Accept()
	if err != nil {
		b.recordAudit(name, actor, actorType, remoteAddr, "error", err.Error())
		return
	}
	b.recordAudit(name, actor, actorType, remoteAddr, "success", "")
	go ssh.DiscardRequests(channelRequests)

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
	if b.Audit == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := b.Audit.RecordAudit(ctx, metadata.Event{
		Namespace:  b.Manager.Namespace,
		Actor:      actor,
		ActorType:  actorType,
		Action:     "ssh.connect",
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
