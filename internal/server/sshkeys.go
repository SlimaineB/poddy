package server

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"poddy/internal/userkeys"
)

type sshKeyInfo struct {
	ID          string `json:"id"`
	Fingerprint string `json:"fingerprint"`
	Type        string `json:"type"`
	Comment     string `json:"comment,omitempty"`
	AddedAt     string `json:"addedAt,omitempty"`
}

func sshKeyInfoFor(key userkeys.Key) sshKeyInfo {
	return sshKeyInfo{
		ID:          key.ID,
		Fingerprint: key.Fingerprint,
		Type:        key.Type,
		Comment:     "user:" + key.Owner,
		AddedAt:     key.AddedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
}

func (s *Server) sshKeysReady(w http.ResponseWriter) bool {
	if s.UserKeys == nil || s.SSHKeys == nil {
		http.Error(w, "SSH keys are not enabled", http.StatusNotFound)
		return false
	}
	return true
}

func (s *Server) listSSHKeys(w http.ResponseWriter, r *http.Request) {
	if !s.sshKeysReady(w) {
		return
	}
	id, ok := s.currentIdentity(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	keys, err := s.UserKeys.ListForOwner(r.Context(), ownerID(id.User))
	if err != nil {
		log.Printf("ssh keys: list for %s: %v", ownerID(id.User), err)
		http.Error(w, "cannot load SSH keys", http.StatusInternalServerError)
		return
	}
	out := make([]sshKeyInfo, 0, len(keys))
	for _, key := range keys {
		out = append(out, sshKeyInfoFor(key))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) addSSHKey(w http.ResponseWriter, r *http.Request) {
	if !s.sshKeysReady(w) {
		return
	}
	id, ok := s.currentIdentity(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var req struct {
		Key string `json:"key"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	key, err := s.UserKeys.Add(r.Context(), ownerID(id.User), req.Key)
	switch {
	case errors.Is(err, userkeys.ErrDuplicate):
		http.Error(w, err.Error(), http.StatusConflict)
		return
	case errors.Is(err, userkeys.ErrLimit):
		http.Error(w, err.Error(), http.StatusConflict)
		return
	case err != nil:
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.reloadSSHKeys(r.Context()); err != nil {
		log.Printf("ssh keys: reload after add: %v", err)
	}
	if s.Workspaces != nil {
		if err := s.Workspaces.SyncSSHKeysForOwner(r.Context(), ownerID(id.User)); err != nil {
			log.Printf("ssh keys: sync workspaces after add: %v", err)
		}
	}
	s.hubSyncSSHKeys(r.Context(), ownerID(id.User))
	writeJSON(w, http.StatusCreated, sshKeyInfoFor(key))
}

func (s *Server) removeSSHKey(w http.ResponseWriter, r *http.Request) {
	if !s.sshKeysReady(w) {
		return
	}
	id, ok := s.currentIdentity(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	err := s.UserKeys.Remove(r.Context(), ownerID(id.User), r.PathValue("id"))
	if errors.Is(err, userkeys.ErrNotFound) {
		http.Error(w, "SSH key not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.reloadSSHKeys(r.Context()); err != nil {
		log.Printf("ssh keys: reload after remove: %v", err)
	}
	if s.Workspaces != nil {
		if err := s.Workspaces.SyncSSHKeysForOwner(r.Context(), ownerID(id.User)); err != nil {
			log.Printf("ssh keys: sync workspaces after remove: %v", err)
		}
	}
	s.hubSyncSSHKeys(r.Context(), ownerID(id.User))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) reloadSSHKeys(ctx context.Context) error {
	keys, err := s.UserKeys.List(ctx)
	if err != nil {
		return err
	}
	return s.SSHKeys.Update(s.SSHAdminKeys, userkeys.Owned(keys), s.SSHAllowUnmapped)
}
