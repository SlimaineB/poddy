package server

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"poddy/internal/auth"
	"poddy/internal/sshca"
	"poddy/internal/sshkeys"
	"poddy/internal/userkeys"
)

func TestDesktopEnrollmentEndToEnd(t *testing.T) {
	for _, tc := range []struct {
		name string
		ttl  time.Duration
	}{
		{name: "configured", ttl: 12 * time.Hour},
		{name: "default", ttl: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, ca := desktopIntegrationServer(t, tc.ttl)
			alice := sessionFor(t, s, auth.User{Username: "alice", Email: "alice@example.com"})
			rec := requestAs(t, s, alice, http.MethodPost, "/api/workspaces", map[string]any{
				"name": "alice-ws", "template": "base",
			})
			if rec.Code != http.StatusCreated {
				t.Fatalf("create = %d, body = %s", rec.Code, rec.Body.String())
			}
			rec = requestAs(t, s, alice, http.MethodPost, "/api/workspaces/alice-ws/desktop", nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("desktop = %d, body = %s", rec.Code, rec.Body.String())
			}
			payload := decodeDesktopPayload(t, rec)

			_, private, err := ed25519.GenerateKey(rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			signer, err := ssh.NewSignerFromKey(private)
			if err != nil {
				t.Fatal(err)
			}
			publicKey := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))

			cert := enrollDesktopCertificate(t, s, payload.Ticket, publicKey)
			if cert.KeyId != "alice@example.com" {
				t.Fatalf("certificate key id = %q", cert.KeyId)
			}
			if got := sshca.Workspace(cert); got != "alice-ws" {
				t.Fatalf("certificate workspace = %q", got)
			}
			if len(cert.ValidPrincipals) != 1 || cert.ValidPrincipals[0] != sshca.WorkspacePrincipal {
				t.Fatalf("certificate principals = %v", cert.ValidPrincipals)
			}
			if err := ca.VerifyCert(cert); err != nil {
				t.Fatalf("issued certificate does not verify: %v", err)
			}
			validFor := time.Until(time.Unix(int64(cert.ValidBefore), 0))
			if validFor < 11*time.Hour {
				t.Fatalf("desktop certificate is valid for %s, want at least 11h", validFor.Round(time.Second))
			}

			// Re-enrolling the same machine key is the normal reconnect path and
			// must return a fresh certificate rather than failing as a duplicate.
			again := enrollDesktopCertificate(t, s, payload.Ticket, publicKey)
			if again.KeyId != cert.KeyId || sshca.Workspace(again) != "alice-ws" {
				t.Fatalf("re-enrolled certificate = %+v", again)
			}
			keys, err := s.UserKeys.ListForOwner(context.Background(), "alice@example.com")
			if err != nil {
				t.Fatal(err)
			}
			if len(keys) != 1 {
				t.Fatalf("enrolled keys = %d, want 1", len(keys))
			}

			assertCertificateAuthenticates(t, ca, cert, signer, "alice-ws")
		})
	}
}

func desktopIntegrationServer(t *testing.T, ttl time.Duration) (*Server, *sshca.CA) {
	t.Helper()
	s := newRBACServer(t, rbacTestConfig())
	ca, err := sshca.Generate()
	if err != nil {
		t.Fatal(err)
	}
	keyring, err := sshkeys.NewDynamic(nil, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	s.DesktopSecret = []byte("desktop-integration-secret")
	s.SSHHost = "portal.example"
	s.SSHPort = "2222"
	s.SSHEnabled = true
	s.SSHCA = ca
	s.SSHCertTTL = ttl
	s.UserKeys = userkeys.NewStore(s.Workspaces.Client, "poddy", "")
	s.SSHKeys = keyring
	s.Workspaces.SSHKeys = keyring
	s.Workspaces.SSHCA = ca
	return s, ca
}

func enrollDesktopCertificate(t *testing.T, s *Server, ticket, publicKey string) *ssh.Certificate {
	t.Helper()
	rec := requestAs(t, s, nil, http.MethodPost, "/api/desktop/enroll", map[string]any{
		"ticket": ticket, "publicKey": publicKey,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("enroll = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Certificate string `json:"certificate"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(resp.Certificate) == "" {
		t.Fatalf("enroll response has no certificate: %s", rec.Body.String())
	}
	parsed, _, _, _, err := ssh.ParseAuthorizedKey([]byte(resp.Certificate))
	if err != nil {
		t.Fatal(err)
	}
	cert, ok := parsed.(*ssh.Certificate)
	if !ok {
		t.Fatalf("enroll returned %T, want *ssh.Certificate", parsed)
	}
	return cert
}

func assertCertificateAuthenticates(t *testing.T, ca *sshca.CA, cert *ssh.Certificate, signer ssh.Signer, workspace string) {
	t.Helper()
	_, hostPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	hostSigner, err := ssh.NewSignerFromKey(hostPrivate)
	if err != nil {
		t.Fatal(err)
	}
	config := &ssh.ServerConfig{
		PublicKeyCallback: func(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			presented, ok := key.(*ssh.Certificate)
			if !ok {
				return nil, fmt.Errorf("client presented %T, want an SSH certificate", key)
			}
			if err := ca.VerifyCert(presented); err != nil {
				return nil, err
			}
			if sshca.Workspace(presented) != workspace {
				return nil, fmt.Errorf("certificate workspace = %q, want %q", sshca.Workspace(presented), workspace)
			}
			if conn.User() != sshca.WorkspacePrincipal {
				return nil, fmt.Errorf("client user = %q", conn.User())
			}
			return nil, nil
		},
	}
	config.AddHostKey(hostSigner)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	serverDone := make(chan error, 1)
	go func() {
		raw, err := listener.Accept()
		if err != nil {
			serverDone <- err
			return
		}
		defer raw.Close()
		conn, channels, requests, err := ssh.NewServerConn(raw, config)
		if err != nil {
			serverDone <- err
			return
		}
		defer conn.Close()
		go ssh.DiscardRequests(requests)
		go func() {
			for channel := range channels {
				_ = channel.Reject(ssh.UnknownChannelType, "test server")
			}
		}()
		serverDone <- nil
	}()

	certSigner, err := ssh.NewCertSigner(cert, signer)
	if err != nil {
		t.Fatal(err)
	}
	client, err := ssh.Dial("tcp", listener.Addr().String(), &ssh.ClientConfig{
		User:            sshca.WorkspacePrincipal,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(certSigner)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	})
	if err != nil {
		t.Fatalf("certificate authentication failed: %v", err)
	}
	_ = client.Close()
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatalf("SSH server handshake failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SSH server handshake timed out")
	}
}
