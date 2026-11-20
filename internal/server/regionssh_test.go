package server

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"net/http/httptest"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"poddy/internal/sshca"
	regiontunnel "poddy/internal/tunnel"
)

func testCertificate(t *testing.T, ca *sshca.CA, owner, workspace string, admin bool) string {
	t.Helper()
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ssh.NewPublicKey(public)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := ca.Sign(key, sshca.WorkspacePrincipal, owner, workspace, admin, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(cert.Marshal())
}

func TestRegionSSHCertificate(t *testing.T) {
	ca, err := sshca.Generate()
	if err != nil {
		t.Fatal(err)
	}
	srv := &Server{SSHCA: ca}

	encoded := testCertificate(t, ca, "alice", "demo", false)
	r := httptest.NewRequest("GET", "/api/internal/region/ssh?workspace=demo", nil)
	r.Header.Set(regiontunnel.RegionSSHCertHeader, encoded)
	actor, admin, err := srv.regionSSHCertificate(r, "demo")
	if err != nil || actor != "alice" || admin {
		t.Fatalf("actor=%q admin=%v err=%v", actor, admin, err)
	}

	adminEncoded := testCertificate(t, ca, "bob", "demo", true)
	r = httptest.NewRequest("GET", "/api/internal/region/ssh?workspace=demo", nil)
	r.Header.Set(regiontunnel.RegionSSHCertHeader, adminEncoded)
	actor, admin, err = srv.regionSSHCertificate(r, "demo")
	if err != nil || actor != "bob" || !admin {
		t.Fatalf("admin actor=%q admin=%v err=%v", actor, admin, err)
	}

	otherScope := testCertificate(t, ca, "alice", "other", false)
	r = httptest.NewRequest("GET", "/api/internal/region/ssh?workspace=demo", nil)
	r.Header.Set(regiontunnel.RegionSSHCertHeader, otherScope)
	if _, _, err := srv.regionSSHCertificate(r, "demo"); err == nil {
		t.Fatal("certificate for another workspace was accepted")
	}

	r = httptest.NewRequest("GET", "/api/internal/region/ssh?workspace=demo", nil)
	if _, _, err := srv.regionSSHCertificate(r, "demo"); err == nil {
		t.Fatal("missing certificate was accepted")
	}

	otherCA, err := sshca.Generate()
	if err != nil {
		t.Fatal(err)
	}
	forged := testCertificate(t, otherCA, "mallory", "demo", false)
	r = httptest.NewRequest("GET", "/api/internal/region/ssh?workspace=demo", nil)
	r.Header.Set(regiontunnel.RegionSSHCertHeader, forged)
	if _, _, err := srv.regionSSHCertificate(r, "demo"); err == nil {
		t.Fatal("certificate from another CA was accepted")
	}
}
