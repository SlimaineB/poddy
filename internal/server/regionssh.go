package server

import (
	"encoding/base64"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"

	"golang.org/x/crypto/ssh"

	"poddy/internal/auth"
	"poddy/internal/region"
	"poddy/internal/sshca"
	regiontunnel "poddy/internal/tunnel"
)

// regionSSH relays an SSH channel from the hub bastion to a workspace that
// lives in a region. The bastion authenticates the user once; this endpoint
// re-verifies the same short-lived certificate, checks the region ownership
// rules, then opens a "tcp:<workspace>:22" stream through the existing
// outbound region tunnel. No inbound connectivity to the region is required.
func (s *Server) regionSSH(w http.ResponseWriter, r *http.Request) {
	if s.Regions == nil || s.RegionTunnel == nil || s.SSHCA == nil {
		http.Error(w, "region SSH relay is not configured", http.StatusNotFound)
		return
	}
	name := strings.TrimSpace(r.URL.Query().Get("workspace"))
	if name == "" {
		http.Error(w, "workspace is required", http.StatusBadRequest)
		return
	}

	actor, admin, err := s.regionSSHCertificate(r, name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	identity := region.Identity{User: auth.User{Username: actor, Subject: actor}, Admin: admin}
	cfg, info, err := s.Regions.FindWorkspace(r.Context(), name, identity)
	if err != nil {
		if region.IsNotFound(err) {
			http.Error(w, "workspace not found", http.StatusNotFound)
			return
		}
		log.Printf("region ssh relay lookup %s: %v", logValue(name), logValue(err.Error()))
		http.Error(w, "region unavailable", http.StatusBadGateway)
		return
	}

	owner := strings.TrimSpace(info.Owner)
	if owner != "" && !strings.EqualFold(owner, actor) {
		if !admin {
			http.Error(w, "workspace not found", http.StatusNotFound)
			return
		}
		if info.Access == nil || !info.Access.Allowed {
			http.Error(w, "owner approval is required", http.StatusForbidden)
			return
		}
	}

	stream, err := s.RegionTunnel.Dial(r.Context(), cfg.Name, regiontunnel.WorkspaceTarget(name, 22))
	if err != nil {
		log.Printf("region ssh relay %s: %v", logValue(name), logValue(err.Error()))
		http.Error(w, "region unavailable", http.StatusBadGateway)
		return
	}
	defer stream.Close()

	conn, err := regiontunnel.AcceptConn(w, r)
	if err != nil {
		return
	}
	defer conn.Close()

	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(conn, stream)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(stream, conn)
		done <- struct{}{}
	}()
	<-done
}

func logValue(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "\n", " "), "\r", " ")
}

func (s *Server) regionSSHCertificate(r *http.Request, workspace string) (string, bool, error) {
	encoded := strings.TrimSpace(r.Header.Get(regiontunnel.RegionSSHCertHeader))
	if encoded == "" {
		return "", false, errors.New("SSH certificate is required")
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", false, errors.New("invalid SSH certificate")
	}
	public, err := ssh.ParsePublicKey(raw)
	if err != nil {
		return "", false, errors.New("invalid SSH certificate")
	}
	cert, ok := public.(*ssh.Certificate)
	if !ok {
		return "", false, errors.New("invalid SSH certificate")
	}
	if err := s.SSHCA.VerifyCert(cert); err != nil {
		return "", false, errors.New("invalid SSH certificate")
	}
	if len(cert.ValidPrincipals) != 1 || cert.ValidPrincipals[0] != sshca.WorkspacePrincipal {
		return "", false, errors.New("invalid SSH certificate")
	}
	actor := strings.TrimSpace(cert.KeyId)
	if actor == "" {
		return "", false, errors.New("invalid SSH certificate")
	}
	if scope := strings.TrimSpace(sshca.Workspace(cert)); scope != "" && !strings.EqualFold(scope, workspace) {
		return "", false, errors.New("certificate is not valid for this workspace")
	}
	return actor, sshca.Admin(cert), nil
}
