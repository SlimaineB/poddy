package server

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"poddy/internal/region"
	"poddy/internal/sshca"
	"poddy/internal/sshkeys"
)

type sshCertificateResponse struct {
	Certificate string `json:"certificate"`
	Principal   string `json:"principal"`
	Owner       string `json:"owner"`
	Workspace   string `json:"workspace,omitempty"`
	ExpiresAt   string `json:"expiresAt"`
	Fingerprint string `json:"fingerprint"`
}

func (s *Server) issueSSHCertificate(w http.ResponseWriter, r *http.Request) {
	if !s.SSHEnabled || s.SSHCA == nil {
		http.Error(w, "SSH certificates are not configured", http.StatusNotImplemented)
		return
	}
	id, ok := s.currentIdentity(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var req struct {
		Key       string `json:"key"`
		PublicKey string `json:"publicKey"`
		Workspace string `json:"workspace"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Key) == "" {
		req.Key = req.PublicKey
	}
	normalized, fingerprint, _, _, err := sshkeys.ParsePublicKey(req.Key)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	public, _, _, _, err := ssh.ParseAuthorizedKey([]byte(normalized))
	if err != nil {
		http.Error(w, "invalid public key", http.StatusBadRequest)
		return
	}

	workspace := strings.TrimSpace(req.Workspace)
	cfg, err := s.loadRBAC(r)
	if err != nil {
		http.Error(w, "rbac: "+err.Error(), http.StatusInternalServerError)
		return
	}
	owner := ownerID(id.User)
	if workspace != "" {
		allowed, err := s.workspaceAllowedAny(r, workspace, id, cfg)
		if err != nil || !allowed {
			if s.Regions == nil {
				http.Error(w, "workspace not found", http.StatusNotFound)
				return
			}
			if _, _, regionErr := s.Regions.FindWorkspace(r.Context(), workspace, s.hubIdentity(id, s.isAdmin(id, cfg))); regionErr != nil {
				if region.IsNotFound(regionErr) {
					http.Error(w, "workspace not found", http.StatusNotFound)
					return
				}
				log.Printf("ssh certificate: region lookup %s: %v", workspace, regionErr)
				http.Error(w, "region unavailable", http.StatusBadGateway)
				return
			}
		}
	}

	ttl := s.SSHCertTTL
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	cert, err := s.SSHCA.Sign(public, sshca.WorkspacePrincipal, owner, workspace, s.isAdmin(id, cfg), ttl)
	if err != nil {
		log.Printf("ssh certificate: sign for %s: %v", owner, err)
		http.Error(w, "cannot issue SSH certificate", http.StatusInternalServerError)
		return
	}

	setAuditTarget(r, workspace)
	setAuditDetail(r, "fingerprint", fingerprint)
	setAuditDetail(r, "expiresAt", time.Unix(int64(cert.ValidBefore), 0).UTC().Format(time.RFC3339))
	writeJSON(w, http.StatusOK, sshCertificateResponse{
		Certificate: strings.TrimSpace(string(ssh.MarshalAuthorizedKey(cert))),
		Principal:   sshca.WorkspacePrincipal,
		Owner:       owner,
		Workspace:   workspace,
		ExpiresAt:   time.Unix(int64(cert.ValidBefore), 0).UTC().Format(time.RFC3339),
		Fingerprint: fingerprint,
	})
}
