package server

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"poddy/internal/k8s"
	"poddy/internal/rbac"
	"poddy/internal/region"
)

type volumeSummary struct {
	Total           int   `json:"total"`
	Assigned        int   `json:"assigned"`
	Unassigned      int   `json:"unassigned"`
	Bytes           int64 `json:"bytes"`
	AssignedBytes   int64 `json:"assignedBytes"`
	UnassignedBytes int64 `json:"unassignedBytes"`
}

func (s *Server) volumes(w http.ResponseWriter, r *http.Request) {
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
	admin := s.isAdmin(id, cfg)
	identity := s.hubIdentity(id, admin)
	regionName := regionQuery(r)
	if s.Regions != nil && regionName != "" {
		if _, ok := s.Regions.Get(regionName); !ok {
			http.Error(w, "unknown region", http.StatusBadRequest)
			return
		}
	}

	var volumes []k8s.Volume
	backupEnabled := s.Backups != nil && s.Backups.Enabled()
	if s.Regions != nil {
		if regionName != "" {
			volumes, backupEnabled, err = s.Regions.VolumesRegion(r.Context(), regionName, identity)
		} else {
			var problems []error
			volumes, backupEnabled, problems = s.Regions.Volumes(r.Context(), identity)
			for _, problem := range problems {
				log.Printf("region volumes: %v", problem)
			}
		}
	} else {
		volumes, err = s.Workspaces.ListVolumes(r.Context())
		if err == nil && regionName != "" && s.Workspaces.Region != "" && s.Workspaces.Region != regionName {
			volumes = nil
		}
	}
	if err != nil {
		if regionName != "" {
			writeVolumeRegionError(w, err)
			return
		}
		writeK8sError(w, err, http.StatusInternalServerError)
		return
	}

	filtered := make([]k8s.Volume, 0, len(volumes))
	summary := volumeSummary{}
	for _, volume := range volumes {
		if !volumeAllowed(volume, id, cfg, admin) {
			continue
		}
		if query := searchQuery(r); query != "" && !matchesSearch(query, volume.Name, volume.Workspace, volume.Owner, volume.Region, volume.Phase, volume.StorageClass) {
			continue
		}
		filtered = append(filtered, volume)
		summary.Total++
		summary.Bytes += volume.Bytes
		if volume.Assigned {
			summary.Assigned++
			summary.AssignedBytes += volume.Bytes
		} else {
			summary.Unassigned++
			summary.UnassignedBytes += volume.Bytes
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"volumes": filtered,
		"summary": summary,
		"backup":  backupEnabled,
	})
}

func volumeAllowed(volume k8s.Volume, id *identity, cfg rbac.Config, admin bool) bool {
	if admin {
		return true
	}
	if !volume.Assigned {
		return false
	}
	return volume.Owner != "" && volume.Owner == ownerID(id.User)
}

func (s *Server) deleteVolume(w http.ResponseWriter, r *http.Request) {
	if s.proxyVolume(w, r) {
		return
	}
	id, cfg, ok := s.volumeRequest(w, r)
	if !ok {
		return
	}
	name := r.PathValue("name")
	volume, allowed, err := s.findVolume(r.Context(), name, id, cfg)
	if err != nil {
		writeK8sError(w, err, http.StatusInternalServerError)
		return
	}
	if !allowed {
		http.Error(w, "volume not found", http.StatusNotFound)
		return
	}
	if volume.Assigned {
		http.Error(w, "volume is assigned to a workspace", http.StatusConflict)
		return
	}
	if err := s.Workspaces.DeleteVolume(r.Context(), name); err != nil {
		switch {
		case errors.Is(err, k8s.ErrVolumeAssigned):
			http.Error(w, "volume is assigned to a workspace", http.StatusConflict)
		case errors.Is(err, k8s.ErrVolumeBusy):
			http.Error(w, "volume is busy with another operation", http.StatusConflict)
		case apierrors.IsNotFound(err):
			http.Error(w, "volume not found", http.StatusNotFound)
		default:
			writeK8sError(w, err, http.StatusInternalServerError)
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) volumeBackups(w http.ResponseWriter, r *http.Request) {
	if s.proxyVolume(w, r) {
		return
	}
	id, cfg, ok := s.volumeRequest(w, r)
	if !ok {
		return
	}
	if !s.backupEnabled(w) {
		return
	}
	name := r.PathValue("name")
	volume, allowed, err := s.findVolume(r.Context(), name, id, cfg)
	if err != nil {
		writeK8sError(w, err, http.StatusInternalServerError)
		return
	}
	if !allowed {
		http.Error(w, "volume not found", http.StatusNotFound)
		return
	}
	objects, err := s.Backups.List(r.Context(), volume.Name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, objects)
}

func (s *Server) createVolumeBackup(w http.ResponseWriter, r *http.Request) {
	if s.proxyVolume(w, r) {
		return
	}
	id, cfg, ok := s.volumeRequest(w, r)
	if !ok {
		return
	}
	if !s.backupEnabled(w) {
		return
	}
	name := r.PathValue("name")
	volume, allowed, err := s.findVolume(r.Context(), name, id, cfg)
	if err != nil {
		writeK8sError(w, err, http.StatusInternalServerError)
		return
	}
	if !allowed {
		http.Error(w, "volume not found", http.StatusNotFound)
		return
	}
	if !volumeReadyForArchive(w, volume) {
		return
	}

	key := s.Backups.Key(volume.Name, time.Now())
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Minute)
	defer cancel()
	if err := s.Workspaces.BackupVolume(ctx, volume.Name, key, s.Backups); err != nil {
		writeVolumeOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"key":  key,
		"name": key[strings.LastIndex(key, "/")+1:],
	})
}

func (s *Server) deleteVolumeBackup(w http.ResponseWriter, r *http.Request) {
	if s.proxyVolume(w, r) {
		return
	}
	id, cfg, ok := s.volumeRequest(w, r)
	if !ok {
		return
	}
	if !s.backupEnabled(w) {
		return
	}
	name := r.PathValue("name")
	volume, allowed, err := s.findVolume(r.Context(), name, id, cfg)
	if err != nil {
		writeK8sError(w, err, http.StatusInternalServerError)
		return
	}
	if !allowed {
		http.Error(w, "volume not found", http.StatusNotFound)
		return
	}
	key := strings.TrimSpace(r.URL.Query().Get("key"))
	if !s.validVolumeKey(volume.Name, key) {
		http.Error(w, "invalid backup key", http.StatusBadRequest)
		return
	}
	if err := s.Backups.Delete(r.Context(), key); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) restoreVolume(w http.ResponseWriter, r *http.Request) {
	if s.proxyVolume(w, r) {
		return
	}
	id, cfg, ok := s.volumeRequest(w, r)
	if !ok {
		return
	}
	if !s.backupEnabled(w) {
		return
	}
	var req struct {
		Key   string `json:"key"`
		Clean bool   `json:"clean"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	name := r.PathValue("name")
	volume, allowed, err := s.findVolume(r.Context(), name, id, cfg)
	if err != nil {
		writeK8sError(w, err, http.StatusInternalServerError)
		return
	}
	if !allowed {
		http.Error(w, "volume not found", http.StatusNotFound)
		return
	}
	if !s.validVolumeKey(volume.Name, req.Key) {
		http.Error(w, "invalid backup key", http.StatusBadRequest)
		return
	}
	if !volumeReadyForArchive(w, volume) {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Minute)
	defer cancel()
	if err := s.Workspaces.RestoreVolume(ctx, volume.Name, req.Key, s.Backups, req.Clean); err != nil {
		writeVolumeOperationError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) volumeRequest(w http.ResponseWriter, r *http.Request) (*identity, rbac.Config, bool) {
	id, ok := s.currentIdentity(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return nil, rbac.Config{}, false
	}
	cfg, err := s.loadRBAC(r)
	if err != nil {
		http.Error(w, "rbac: "+err.Error(), http.StatusInternalServerError)
		return nil, rbac.Config{}, false
	}
	return id, cfg, true
}

func (s *Server) findVolume(ctx context.Context, name string, id *identity, cfg rbac.Config) (k8s.Volume, bool, error) {
	volumes, err := s.Workspaces.ListVolumes(ctx)
	if err != nil {
		return k8s.Volume{}, false, err
	}
	for _, volume := range volumes {
		if volume.Name == name {
			return volume, volumeAllowed(volume, id, cfg, s.isAdmin(id, cfg)), nil
		}
	}
	return k8s.Volume{}, false, nil
}

func (s *Server) backupEnabled(w http.ResponseWriter) bool {
	if s.Backups == nil || !s.Backups.Enabled() {
		http.Error(w, "volume backups are not configured", http.StatusNotImplemented)
		return false
	}
	return true
}

func (s *Server) validVolumeKey(volume, key string) bool {
	if s.Backups == nil || !s.Backups.Enabled() {
		return false
	}
	return s.Backups.ValidKey(key) && strings.HasPrefix(key, s.Backups.VolumePrefix(volume))
}

func volumeReadyForArchive(w http.ResponseWriter, volume k8s.Volume) bool {
	if volume.Phase != "" && volume.Phase != "Bound" {
		http.Error(w, "volume is not bound yet", http.StatusConflict)
		return false
	}
	if volume.Assigned && volume.WorkspacePhase != "Stopped" {
		http.Error(w, "stop the workspace before backing up or restoring its volume", http.StatusConflict)
		return false
	}
	return true
}

func writeVolumeOperationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, k8s.ErrVolumeBusy):
		http.Error(w, "volume is busy with another operation", http.StatusConflict)
	case apierrors.IsNotFound(err):
		http.Error(w, "volume or backup not found", http.StatusNotFound)
	default:
		http.Error(w, err.Error(), http.StatusBadGateway)
	}
}

func writeVolumeRegionError(w http.ResponseWriter, err error) {
	var httpErr *region.HTTPError
	if errors.As(err, &httpErr) {
		if httpErr.Status == http.StatusNotFound {
			http.Error(w, "volume not found", http.StatusNotFound)
			return
		}
		if httpErr.Status >= 400 && httpErr.Status < 500 {
			http.Error(w, httpErr.Message, httpErr.Status)
			return
		}
	}
	log.Printf("region volume request: %v", err)
	http.Error(w, "region unavailable", http.StatusBadGateway)
}

// proxyVolume forwards a volume action to the region that owns the volume. It
// returns true when the request has been handled.
func (s *Server) proxyVolume(w http.ResponseWriter, r *http.Request) bool {
	if s.Regions == nil {
		return false
	}
	id, ok := s.currentIdentity(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return true
	}
	cfg, err := s.loadRBAC(r)
	if err != nil {
		http.Error(w, "rbac: "+err.Error(), http.StatusInternalServerError)
		return true
	}
	identity := s.hubIdentity(id, s.isAdmin(id, cfg))
	regionName := regionQuery(r)
	if regionName == "" {
		regionName, err = s.Regions.FindVolumeRegion(r.Context(), r.PathValue("name"), identity)
		if err != nil {
			writeVolumeRegionError(w, err)
			return true
		}
	}
	if _, ok := s.Regions.Get(regionName); !ok {
		http.Error(w, "unknown region", http.StatusBadRequest)
		return true
	}
	client := s.Regions.Client(regionName)
	if client == nil {
		http.Error(w, "region unavailable", http.StatusBadGateway)
		return true
	}
	client.Proxy(w, r, identity)
	return true
}
