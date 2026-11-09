package sshca

import (
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

const (
	WorkspacePrincipal = "dev"
	workspaceExtension = "poddy-workspace"
	adminExtension     = "poddy-admin"
)

type CA struct {
	signer  ssh.Signer
	private crypto.PrivateKey
	public  ssh.PublicKey
}

func LoadOrGenerate(path string) (*CA, error) {
	if strings.TrimSpace(path) == "" {
		return Generate()
	}
	data, err := os.ReadFile(path)
	if err == nil {
		return NewFromPEM(data)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	ca, err := Generate()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	data = ca.PrivateKeyPEM()
	if len(data) == 0 {
		return nil, errors.New("cannot encode SSH CA key")
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return nil, err
	}
	return ca, nil
}

func Generate() (*CA, error) {
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		return nil, err
	}
	return &CA{signer: signer, private: private, public: signer.PublicKey()}, nil
}

func NewFromPEM(data []byte) (*CA, error) {
	private, err := ssh.ParseRawPrivateKey(data)
	if err != nil {
		return nil, fmt.Errorf("parse SSH CA key: %w", err)
	}
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		return nil, fmt.Errorf("SSH CA signer: %w", err)
	}
	return &CA{signer: signer, private: private, public: signer.PublicKey()}, nil
}

func (c *CA) PrivateKeyPEM() []byte {
	block, err := ssh.MarshalPrivateKey(c.private, "poddy-ssh-ca")
	if err != nil {
		return nil
	}
	return pem.EncodeToMemory(block)
}

func (c *CA) PublicKey() ssh.PublicKey {
	if c == nil {
		return nil
	}
	return c.public
}

func (c *CA) AuthorizedKey() string {
	return c.authorizedKey(true)
}

func (c *CA) AuthorizedKeyUnrestricted() string {
	return c.authorizedKey(false)
}

func (c *CA) authorizedKey(loopbackOnly bool) string {
	if c == nil {
		return ""
	}
	public := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(c.public)))
	from := ""
	if loopbackOnly {
		from = `,from="127.0.0.1,::1"`
	}
	return fmt.Sprintf(`cert-authority,principals="%s"%s %s poddy-ca`, WorkspacePrincipal, from, public)
}

func (c *CA) Sign(public ssh.PublicKey, principal, owner, workspace string, admin bool, ttl time.Duration) (*ssh.Certificate, error) {
	if c == nil {
		return nil, errors.New("SSH CA is not configured")
	}
	if public == nil {
		return nil, errors.New("public key is required")
	}
	if strings.TrimSpace(principal) == "" {
		principal = WorkspacePrincipal
	}
	if strings.TrimSpace(owner) == "" {
		return nil, errors.New("owner is required")
	}
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	now := time.Now()
	cert := &ssh.Certificate{
		Key:             public,
		CertType:        ssh.UserCert,
		KeyId:           strings.TrimSpace(owner),
		ValidPrincipals: []string{strings.TrimSpace(principal)},
		ValidAfter:      uint64(now.Add(-time.Minute).Unix()),
		ValidBefore:     uint64(now.Add(ttl).Unix()),
		Permissions: ssh.Permissions{
			Extensions: map[string]string{},
		},
	}
	if workspace = strings.TrimSpace(workspace); workspace != "" {
		cert.Permissions.Extensions[workspaceExtension] = workspace
	}
	if admin {
		cert.Permissions.Extensions[adminExtension] = "true"
	}
	if err := cert.SignCert(rand.Reader, c.signer); err != nil {
		return nil, err
	}
	return cert, nil
}

func (c *CA) VerifyCert(cert *ssh.Certificate) error {
	if c == nil {
		return errors.New("SSH CA is not configured")
	}
	if cert == nil || cert.CertType != ssh.UserCert {
		return errors.New("not a user certificate")
	}
	if len(cert.ValidPrincipals) == 0 {
		return errors.New("certificate has no principals")
	}
	if cert.SignatureKey == nil || ssh.FingerprintSHA256(cert.SignatureKey) != ssh.FingerprintSHA256(c.public) {
		return errors.New("certificate signed by an unrecognized authority")
	}
	checker := ssh.CertChecker{
		IsUserAuthority: func(auth ssh.PublicKey) bool {
			return ssh.FingerprintSHA256(auth) == ssh.FingerprintSHA256(c.public)
		},
	}
	return checker.CheckCert(cert.ValidPrincipals[0], cert)
}

func Workspace(cert *ssh.Certificate) string {
	if cert == nil {
		return ""
	}
	return cert.Permissions.Extensions[workspaceExtension]
}

func Admin(cert *ssh.Certificate) bool {
	if cert == nil {
		return false
	}
	return cert.Permissions.Extensions[adminExtension] == "true"
}
