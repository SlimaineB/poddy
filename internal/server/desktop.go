package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"poddy/internal/k8s"
	"poddy/internal/userkeys"
)

const (
	desktopTicketTTL      = 2 * time.Minute
	desktopExtensionID    = "slimaineb.poddy-remote"
	desktopRemoteUser     = "dev"
	desktopTicketAudience = "poddy-vscode-desktop"
)

type desktopClaims struct {
	Owner     string `json:"owner"`
	Workspace string `json:"workspace"`
	Audience  string `json:"aud"`
	Expires   int64  `json:"exp"`
}

type desktopPayload struct {
	API       string `json:"api"`
	Workspace string `json:"workspace"`
	User      string `json:"user"`
	SSHHost   string `json:"sshHost"`
	SSHPort   string `json:"sshPort"`
	Ticket    string `json:"ticket"`
}

func (s *Server) desktop(w http.ResponseWriter, r *http.Request) {
	scheme := "vscode"
	switch strings.ToLower(strings.TrimSpace(r.URL.Query().Get("editor"))) {
	case "", "vscode":
	case "cursor":
		scheme = "cursor"
	default:
		http.Error(w, "unsupported editor", http.StatusBadRequest)
		return
	}

	name := r.PathValue("name")
	id, ok := s.currentIdentity(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	cfg, err := s.loadRBAC(r)
	if err != nil {
		http.Error(w, "rbac: "+err.Error(), http.StatusInternalServerError)
		return
	}
	sshHost := s.SSHHost
	sshPort := s.SSHPort
	owner := ""
	if s.Regions != nil {
		regionCfg, info, findErr := s.Regions.FindWorkspace(r.Context(), name, s.hubIdentity(id, s.isAdmin(id, cfg)))
		if findErr != nil {
			writeRegionError(w, findErr)
			return
		}
		owner = info.Owner
		if regionCfg.SSHHost != "" {
			sshHost = regionCfg.SSHHost
		}
		if regionCfg.SSHPort != "" {
			sshPort = regionCfg.SSHPort
		}
	} else {
		if s.Workspaces == nil {
			http.Error(w, "workspace not found", http.StatusNotFound)
			return
		}
		pod, err := s.Workspaces.Get(r.Context(), name)
		if err != nil {
			http.Error(w, "workspace not found", http.StatusNotFound)
			return
		}
		owner = k8s.Owner(pod)
		if !s.isAdmin(id, cfg) && owner != ownerID(id.User) {
			http.Error(w, "workspace not found", http.StatusNotFound)
			return
		}
	}
	if sshHost == "" {
		http.Error(w, "SSH is not configured", http.StatusNotImplemented)
		return
	}

	ticket, err := s.signDesktopTicket(desktopClaims{
		Owner:     owner,
		Workspace: name,
		Audience:  desktopTicketAudience,
		Expires:   time.Now().Add(desktopTicketTTL).Unix(),
	})
	if err != nil {
		http.Error(w, "cannot create ticket", http.StatusInternalServerError)
		return
	}
	payload, err := json.Marshal(desktopPayload{
		API:       requestBaseURL(r),
		Workspace: name,
		User:      desktopRemoteUser,
		SSHHost:   sshHost,
		SSHPort:   sshPort,
		Ticket:    ticket,
	})
	if err != nil {
		http.Error(w, "cannot create ticket", http.StatusInternalServerError)
		return
	}
	url := fmt.Sprintf("%s://%s/connect?payload=%s", scheme, desktopExtensionID, base64.RawURLEncoding.EncodeToString(payload))
	writeJSON(w, http.StatusOK, map[string]string{"url": url})
}

func (s *Server) desktopEnroll(w http.ResponseWriter, r *http.Request) {
	if s.UserKeys == nil || s.SSHKeys == nil {
		http.Error(w, "SSH keys are not enabled", http.StatusNotFound)
		return
	}
	var req struct {
		Ticket    string `json:"ticket"`
		PublicKey string `json:"publicKey"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	claims, err := s.verifyDesktopTicket(req.Ticket)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	if strings.TrimSpace(req.PublicKey) == "" {
		http.Error(w, "public key is required", http.StatusBadRequest)
		return
	}

	if s.Regions != nil {
		_, info, err := s.Regions.FindWorkspace(r.Context(), claims.Workspace, s.hubAdminIdentity())
		if err != nil {
			http.Error(w, "workspace not found", http.StatusNotFound)
			return
		}
		if !strings.EqualFold(strings.TrimSpace(claims.Owner), strings.TrimSpace(info.Owner)) {
			http.Error(w, "workspace not found", http.StatusNotFound)
			return
		}
	} else if s.Workspaces != nil {
		pod, err := s.Workspaces.Get(r.Context(), claims.Workspace)
		if err != nil {
			http.Error(w, "workspace not found", http.StatusNotFound)
			return
		}
		if !strings.EqualFold(strings.TrimSpace(claims.Owner), strings.TrimSpace(k8s.Owner(pod))) {
			http.Error(w, "workspace not found", http.StatusNotFound)
			return
		}
	}

	key, err := s.UserKeys.Add(r.Context(), claims.Owner, req.PublicKey)
	switch {
	case errors.Is(err, userkeys.ErrDuplicate):
		// Reconnecting with the same machine key is the normal case.
		w.WriteHeader(http.StatusNoContent)
		return
	case errors.Is(err, userkeys.ErrLimit):
		http.Error(w, err.Error(), http.StatusConflict)
		return
	case err != nil:
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.reloadSSHKeys(r.Context()); err != nil {
		log.Printf("desktop: reload ssh keys: %v", err)
	}
	if s.Workspaces != nil {
		if err := s.Workspaces.SyncSSHKeysForOwner(r.Context(), claims.Owner); err != nil {
			log.Printf("desktop: sync workspaces: %v", err)
		}
	}
	s.hubSyncSSHKeys(r.Context(), claims.Owner)
	writeJSON(w, http.StatusCreated, sshKeyInfoFor(key))
}

func (s *Server) signDesktopTicket(claims desktopClaims) (string, error) {
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	body := base64.RawURLEncoding.EncodeToString(payload)
	return body + "." + base64.RawURLEncoding.EncodeToString(s.desktopMAC(body)), nil
}

func (s *Server) verifyDesktopTicket(ticket string) (desktopClaims, error) {
	var claims desktopClaims
	parts := strings.Split(ticket, ".")
	if len(parts) != 2 {
		return claims, errors.New("invalid ticket")
	}
	body := parts[0]
	got, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(got) == 0 {
		return claims, errors.New("invalid ticket")
	}
	if !hmac.Equal(got, s.desktopMAC(body)) {
		return claims, errors.New("invalid ticket")
	}
	payload, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return claims, errors.New("invalid ticket")
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return claims, errors.New("invalid ticket")
	}
	if claims.Audience != desktopTicketAudience || claims.Owner == "" || claims.Workspace == "" {
		return claims, errors.New("invalid ticket")
	}
	if time.Now().After(time.Unix(claims.Expires, 0)) {
		return claims, errors.New("ticket expired")
	}
	return claims, nil
}

func (s *Server) desktopMAC(body string) []byte {
	mac := hmac.New(sha256.New, s.DesktopSecret)
	_, _ = mac.Write([]byte(body))
	return mac.Sum(nil)
}
