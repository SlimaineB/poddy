package server

import (
	"encoding/json"
	"net/http"
	"time"

	"poddy/internal/access"
	"poddy/internal/quota"
	"poddy/internal/rbac"
	"poddy/internal/region"
)

// regionControlPlane verifies that the caller is a trusted region process. It is
// used by the internal endpoints that expose shared state to regions, so those
// regions never need direct database access.
func (s *Server) regionControlPlane(r *http.Request) bool {
	if s.Regions == nil {
		return false
	}
	cfg, ok := s.Regions.Get(r.Header.Get(region.RegionNameHeader))
	if !ok {
		return false
	}
	claims, err := region.VerifyIdentity(cfg.Key, r.Header.Get(region.IdentityHeader))
	return err == nil && claims.Admin
}

func (s *Server) regionAccessStore(w http.ResponseWriter) (access.Store, bool) {
	if s.Access == nil {
		http.Error(w, "access store is not configured", http.StatusNotImplemented)
		return nil, false
	}
	return s.Access, true
}

func (s *Server) regionAccessState(w http.ResponseWriter, r *http.Request) {
	if !s.regionControlPlane(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	store, ok := s.regionAccessStore(w)
	if !ok {
		return
	}
	state, err := store.Load(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (s *Server) regionAccessRequest(w http.ResponseWriter, r *http.Request) {
	if !s.regionControlPlane(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	store, ok := s.regionAccessStore(w)
	if !ok {
		return
	}
	actor, ok := decodeRegionActor(w, r)
	if !ok {
		return
	}
	granted, err := store.Request(r.Context(), r.PathValue("workspace"), actor)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"granted": granted})
}

func (s *Server) regionAccessApprove(w http.ResponseWriter, r *http.Request) {
	if !s.regionControlPlane(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	store, ok := s.regionAccessStore(w)
	if !ok {
		return
	}
	var req struct {
		Actor      string `json:"actor"`
		Approver   string `json:"approver"`
		TTLSeconds int    `json:"ttlSeconds"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	actor := accessActor(req.Actor)
	if actor == "" {
		http.Error(w, "actor is required", http.StatusBadRequest)
		return
	}
	ttl := time.Duration(req.TTLSeconds) * time.Second
	if ttl <= 0 {
		ttl = access.DefaultAccessGrantTTL
	}
	if err := store.Approve(r.Context(), r.PathValue("workspace"), actor, accessActor(req.Approver), ttl); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) regionAccessDeny(w http.ResponseWriter, r *http.Request) {
	s.regionAccessMutate(w, r, "deny")
}

func (s *Server) regionAccessRevoke(w http.ResponseWriter, r *http.Request) {
	s.regionAccessMutate(w, r, "revoke")
}

func (s *Server) regionAccessMutate(w http.ResponseWriter, r *http.Request, action string) {
	if !s.regionControlPlane(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	store, ok := s.regionAccessStore(w)
	if !ok {
		return
	}
	actor, ok := decodeRegionActor(w, r)
	if !ok {
		return
	}
	workspace := r.PathValue("workspace")
	var err error
	switch action {
	case "deny":
		err = store.Deny(r.Context(), workspace, actor)
	case "revoke":
		err = store.Revoke(r.Context(), workspace, actor)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) regionAccessPresence(w http.ResponseWriter, r *http.Request) {
	if !s.regionControlPlane(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	store, ok := s.regionAccessStore(w)
	if !ok {
		return
	}
	var presence access.Presence
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&presence); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	if err := store.SetPresence(r.Context(), r.PathValue("workspace"), r.PathValue("session"), presence); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) regionAccessClearPresence(w http.ResponseWriter, r *http.Request) {
	if !s.regionControlPlane(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	store, ok := s.regionAccessStore(w)
	if !ok {
		return
	}
	if err := store.ClearPresence(r.Context(), r.PathValue("workspace"), r.PathValue("session")); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) regionAccessDeleteWorkspace(w http.ResponseWriter, r *http.Request) {
	if !s.regionControlPlane(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	store, ok := s.regionAccessStore(w)
	if !ok {
		return
	}
	if err := store.DeleteWorkspace(r.Context(), r.PathValue("workspace")); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func decodeRegionActor(w http.ResponseWriter, r *http.Request) (string, bool) {
	var req struct {
		Actor string `json:"actor"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return "", false
	}
	actor := accessActor(req.Actor)
	if actor == "" {
		http.Error(w, "actor is required", http.StatusBadRequest)
		return "", false
	}
	return actor, true
}

func (s *Server) regionQuotaStore(w http.ResponseWriter) (quota.Store, bool) {
	if s.Quota == nil {
		http.Error(w, "quota store is not configured", http.StatusNotImplemented)
		return nil, false
	}
	return s.Quota, true
}

func (s *Server) regionUserQuotas(w http.ResponseWriter, r *http.Request) {
	if !s.regionControlPlane(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	store, ok := s.regionQuotaStore(w)
	if !ok {
		return
	}
	quotas, err := store.UserQuotas(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, quotas)
}

func (s *Server) regionTeamQuotas(w http.ResponseWriter, r *http.Request) {
	if !s.regionControlPlane(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	store, ok := s.regionQuotaStore(w)
	if !ok {
		return
	}
	quotas, err := store.TeamQuotas(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, quotas)
}

func (s *Server) regionSetUserQuota(w http.ResponseWriter, r *http.Request) {
	s.regionSetQuota(w, r, true)
}

func (s *Server) regionSetTeamQuota(w http.ResponseWriter, r *http.Request) {
	s.regionSetQuota(w, r, false)
}

func (s *Server) regionSetQuota(w http.ResponseWriter, r *http.Request, user bool) {
	if !s.regionControlPlane(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	store, ok := s.regionQuotaStore(w)
	if !ok {
		return
	}
	var value rbac.Quota
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&value); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	value.Normalize()
	if err := value.Validate(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var err error
	if user {
		identity := r.PathValue("identity")
		if value.IsZero() {
			err = store.DeleteUserQuota(r.Context(), identity)
		} else {
			err = store.SetUserQuota(r.Context(), identity, value)
		}
	} else {
		team := r.PathValue("team")
		if value.IsZero() {
			err = store.DeleteTeamQuota(r.Context(), team)
		} else {
			err = store.SetTeamQuota(r.Context(), team, value)
		}
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) regionDeleteUserQuota(w http.ResponseWriter, r *http.Request) {
	s.regionDeleteQuota(w, r, true)
}

func (s *Server) regionDeleteTeamQuota(w http.ResponseWriter, r *http.Request) {
	s.regionDeleteQuota(w, r, false)
}

func (s *Server) regionDeleteQuota(w http.ResponseWriter, r *http.Request, user bool) {
	if !s.regionControlPlane(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	store, ok := s.regionQuotaStore(w)
	if !ok {
		return
	}
	var err error
	if user {
		err = store.DeleteUserQuota(r.Context(), r.PathValue("identity"))
	} else {
		err = store.DeleteTeamQuota(r.Context(), r.PathValue("team"))
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
