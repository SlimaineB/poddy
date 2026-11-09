package server

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"poddy/internal/sshca"
)

func TestIssueSSHCertificate(t *testing.T) {
	ca, err := sshca.Generate()
	if err != nil {
		t.Fatal(err)
	}
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]string{"key": string(ssh.MarshalAuthorizedKey(signer.PublicKey()))})
	if err != nil {
		t.Fatal(err)
	}
	srv := &Server{SSHEnabled: true, SSHCA: ca, SSHCertTTL: 5 * time.Minute}
	req := httptest.NewRequest(http.MethodPost, "/api/me/ssh-certificate", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var response sshCertificateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Certificate == "" {
		t.Fatal("empty certificate")
	}
	public, _, _, _, err := ssh.ParseAuthorizedKey([]byte(response.Certificate))
	if err != nil {
		t.Fatal(err)
	}
	cert, ok := public.(*ssh.Certificate)
	if !ok {
		t.Fatalf("expected certificate, got %T", public)
	}
	if err := ca.VerifyCert(cert); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if cert.KeyId != "local" {
		t.Fatalf("key id = %q", cert.KeyId)
	}
	if len(cert.ValidPrincipals) != 1 || cert.ValidPrincipals[0] != sshca.WorkspacePrincipal {
		t.Fatalf("principals = %v", cert.ValidPrincipals)
	}
}

func TestIssueSSHCertificateRejectsInvalidKey(t *testing.T) {
	ca, err := sshca.Generate()
	if err != nil {
		t.Fatal(err)
	}
	srv := &Server{SSHEnabled: true, SSHCA: ca, SSHCertTTL: time.Minute}
	req := httptest.NewRequest(http.MethodPost, "/api/me/ssh-certificate", bytes.NewBufferString(`{"key":"not-a-key"}`))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}
