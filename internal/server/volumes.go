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
	if !volumeReadyForBackup(w, volume) {
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

type restoreVolumeRequest struct {
	Key          string `json:"key"`
	Clean        bool   `json:"clean"`
	Target       string `json:"target"`
	TargetRegion string `json:"targetRegion"`
}

func (s *Server) restoreVolume(w http.ResponseWriter, r *http.Request) {
	var req restoreVolumeRequest
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	source := r.PathValue("name")
	target := strings.TrimSpace(req.Target)
	if target == "" {
		target = source
	}
	if s.Regions != nil {
		s.restoreVolumeHub(w, r, source, target, strings.TrimSpace(req.TargetRegion), req.Key, req.Clean)
		return
	}
	id, cfg, ok := s.volumeRequest(w, r)
	if !ok {
		return
	}
	if !s.backupEnabled(w) {
		return
	}
	s.restoreVolumeInto(w, r, id, cfg, source, target, req.Key, req.Clean)
}

// restoreVolumeHub routes a restore to the region that owns the target volume.
// The backup key still belongs to the source volume, so source access is
// checked on the hub before the target region extracts the archive.
func (s *Server) restoreVolumeHub(w http.ResponseWriter, r *http.Request, source, target, targetRegion, key string, clean bool) {
	id, cfg, ok := s.volumeRequest(w, r)
	if !ok {
		return
	}
	admin := s.isAdmin(id, cfg)
	identity := s.hubIdentity(id, admin)
	sourceCfg, sourceVolume, err := s.Regions.FindVolumeInRegion(r.Context(), regionQuery(r), source, identity)
	if err != nil {
		writeVolumeRegionError(w, err)
		return
	}
	if !volumeAllowed(sourceVolume, id, cfg, admin) {
		http.Error(w, "source volume not found", http.StatusNotFound)
		return
	}
	if targetRegion == "" {
		if target == source {
			targetRegion = sourceCfg.Name
		} else {
			targetRegion, err = s.Regions.FindVolumeRegion(r.Context(), target, identity)
			if err != nil {
				writeVolumeRegionError(w, err)
				return
			}
		}
	}
	if _, ok := s.Regions.Get(targetRegion); !ok {
		http.Error(w, "unknown target region", http.StatusBadRequest)
		return
	}
	client := s.Regions.Client(targetRegion)
	if client == nil {
		http.Error(w, "region unavailable", http.StatusBadGateway)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Minute)
	defer cancel()
	if err := client.RestoreFrom(ctx, target, source, key, clean, identity); err != nil {
		writeVolumeRegionError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// restoreVolumeFrom is called by the hub on the region that owns the target
// volume. The hub has already checked access to the source volume.
func (s *Server) restoreVolumeFrom(w http.ResponseWriter, r *http.Request) {
	if !s.RegionMode {
		http.NotFound(w, r)
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
		Source string `json:"source"`
		Key    string `json:"key"`
		Clean  bool   `json:"clean"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	target := r.PathValue("name")
	source := strings.TrimSpace(req.Source)
	if source == "" {
		source = target
	}
	targetVolume, allowed, err := s.findVolume(r.Context(), target, id, cfg)
	if err != nil {
		writeK8sError(w, err, http.StatusInternalServerError)
		return
	}
	if !allowed {
		http.Error(w, "target volume not found", http.StatusNotFound)
		return
	}
	if !s.validVolumeKey(source, req.Key) {
		http.Error(w, "invalid backup key", http.StatusBadRequest)
		return
	}
	if !volumeReadyForRestore(w, targetVolume) {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Minute)
	defer cancel()
	if err := s.Workspaces.RestoreVolume(ctx, targetVolume.Name, req.Key, s.Backups, req.Clean); err != nil {
		writeVolumeOperationError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) restoreVolumeInto(w http.ResponseWriter, r *http.Request, id *identity, cfg rbac.Config, source, target, key string, clean bool) {
	sourceVolume, allowed, err := s.findVolume(r.Context(), source, id, cfg)
	if err != nil {
		writeK8sError(w, err, http.StatusInternalServerError)
		return
	}
	if !allowed {
		http.Error(w, "source volume not found", http.StatusNotFound)
		return
	}
	targetVolume, allowed, err := s.findVolume(r.Context(), target, id, cfg)
	if err != nil {
		writeK8sError(w, err, http.StatusInternalServerError)
		return
	}
	if !allowed {
		http.Error(w, "target volume not found", http.StatusNotFound)
		return
	}
	if !s.validVolumeKey(sourceVolume.Name, key) {
		http.Error(w, "invalid backup key", http.StatusBadRequest)
		return
	}
	if !volumeReadyForRestore(w, targetVolume) {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Minute)
	defer cancel()
	if err := s.Workspaces.RestoreVolume(ctx, targetVolume.Name, key, s.Backups, clean); err != nil {
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

func volumeReadyForBackup(w http.ResponseWriter, volume k8s.Volume) bool {
	if volume.Phase != "" && volume.Phase != "Bound" {
		http.Error(w, "volume is not bound yet", http.StatusConflict)
		return false
	}
	if !volume.Assigned {
		return true
	}
	switch volume.WorkspacePhase {
	case "Stopped":
		return true
	case "Running":
		if hasAccessMode(volume.AccessModes, "ReadWriteOncePod") {
			http.Error(w, "live backup is not supported for ReadWriteOncePod volumes; stop the workspace first", http.StatusConflict)
			return false
		}
		return true
	default:
		http.Error(w, "wait for the workspace to be running or stopped before backing up its volume", http.StatusConflict)
		return false
	}
}

func volumeReadyForRestore(w http.ResponseWriter, volume k8s.Volume) bool {
	if volume.Phase != "" && volume.Phase != "Bound" {
		http.Error(w, "volume is not bound yet", http.StatusConflict)
		return false
	}
	if volume.Assigned && volume.WorkspacePhase != "Stopped" {
		http.Error(w, "stop the workspace before restoring its volume", http.StatusConflict)
		return false
	}
	return true
}

func hasAccessMode(modes []string, want string) bool {
	for _, mode := range modes {
		if mode == want {
			return true
		}
	}
	return false
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
		if httpErr.Status == http.StatusNotImplemented {
			http.Error(w, httpErr.Message, http.StatusNotImplemented)
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
