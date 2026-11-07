package server

import (
	"log"
	"net/http"
	"sort"
	"strings"

	"poddy/internal/k8s"
	"poddy/internal/region"
)

type topologyResource struct {
	Kind         string   `json:"kind"`
	Name         string   `json:"name"`
	Phase        string   `json:"phase,omitempty"`
	StorageClass string   `json:"storageClass,omitempty"`
	Capacity     string   `json:"capacity,omitempty"`
	Requested    string   `json:"requested,omitempty"`
	AccessModes  []string `json:"accessModes,omitempty"`
	VolumeName   string   `json:"volumeName,omitempty"`
}

type topologyWorkspace struct {
	Name          string             `json:"name"`
	Template      string             `json:"template,omitempty"`
	Phase         string             `json:"phase,omitempty"`
	Image         string             `json:"image,omitempty"`
	Persistent    bool               `json:"persistent"`
	BrowserVSCode bool               `json:"browserVSCode,omitempty"`
	CreatedAt     string             `json:"createdAt,omitempty"`
	Resources     []topologyResource `json:"resources"`
}

type topologyUser struct {
	Identity       string              `json:"identity"`
	Name           string              `json:"name"`
	Current        bool                `json:"current"`
	WorkspaceCount int                 `json:"workspaceCount"`
	VolumeCount    int                 `json:"volumeCount"`
	StorageBytes   int64               `json:"storageBytes"`
	Workspaces     []topologyWorkspace `json:"workspaces"`
}

type topologyTotals struct {
	Users        int   `json:"users"`
	Workspaces   int   `json:"workspaces"`
	Volumes      int   `json:"volumes"`
	StorageBytes int64 `json:"storageBytes"`
}

type topologyResponse struct {
	Totals  topologyTotals `json:"totals"`
	Users   []topologyUser `json:"users"`
	Total   int            `json:"total,omitempty"`
	Page    int            `json:"page,omitempty"`
	PerPage int            `json:"perPage,omitempty"`
	Pages   int            `json:"pages,omitempty"`
}

func (s *Server) topology(w http.ResponseWriter, r *http.Request) {
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
	if s.Regions != nil {
		regionFilter := regionQuery(r)
		var topology region.Topology
		if regionFilter != "" {
			if _, ok := s.Regions.Get(regionFilter); !ok {
				http.Error(w, "unknown region", http.StatusBadRequest)
				return
			}
			topology, err = s.Regions.TopologyRegion(r.Context(), regionFilter, s.hubIdentity(id, s.isAdmin(id, cfg)))
			if err != nil {
				writeRegionError(w, err)
				return
			}
		} else {
			var problems []error
			topology, problems = s.Regions.Topology(r.Context(), s.hubIdentity(id, s.isAdmin(id, cfg)))
			for _, problem := range problems {
				log.Printf("topology: %v", problem)
			}
		}
		query := searchQuery(r)
		if query != "" {
			filtered := make([]region.TopologyUser, 0, len(topology.Users))
			for _, user := range topology.Users {
				values := []string{user.Identity, user.Name}
				for _, workspace := range user.Workspaces {
					values = append(values, workspace.Name, workspace.Region)
				}
				if matchesSearch(query, values...) {
					filtered = append(filtered, user)
				}
			}
			topology.Users = filtered
			if query != "" {
				topology.Totals.Users = len(filtered)
			}
		}
		if pagedRequest(r) {
			page, perPage := pageRequest(r)
			items, info := paginate(topology.Users, page, perPage)
			topology.Users = items
			topology.Total = info.Total
			topology.Page = info.Page
			topology.PerPage = info.PerPage
			topology.Pages = info.Pages
		}
		writeJSON(w, http.StatusOK, topology)
		return
	}
	if s.Workspaces == nil {
		http.Error(w, "workspaces are not configured", http.StatusNotImplemented)
		return
	}

	current := ownerID(id.User)
	admin := s.isAdmin(id, cfg)
	var workspaces []k8s.Workspace
	if admin {
		workspaces, err = s.Workspaces.List(r.Context())
	} else {
		workspaces, err = s.Workspaces.ListByOwner(r.Context(), current)
	}
	if err != nil {
		writeK8sError(w, err, http.StatusInternalServerError)
		return
	}

	volumes, err := s.Workspaces.ListVolumes(r.Context())
	if err != nil {
		log.Printf("topology: list volumes: %v", err)
		volumes = nil
	}
	byWorkspace := make(map[string][]k8s.Volume, len(volumes))
	for _, volume := range volumes {
		byWorkspace[volume.Workspace] = append(byWorkspace[volume.Workspace], volume)
	}

	users := map[string]*topologyUser{}
	ensureUser := func(identity string) *topologyUser {
		identity = strings.TrimSpace(identity)
		if identity == "" {
			identity = "unknown"
		}
		user := users[identity]
		if user == nil {
			user = &topologyUser{
				Identity:   identity,
				Name:       ownerDisplayName(identity),
				Current:    strings.EqualFold(identity, current),
				Workspaces: []topologyWorkspace{},
			}
			users[identity] = user
		}
		return user
	}
	if !admin {
		ensureUser(current)
	}

	resp := topologyResponse{Users: []topologyUser{}}
	for _, workspace := range workspaces {
		user := ensureUser(workspace.Owner)
		node := topologyWorkspace{
			Name:          workspace.Name,
			Template:      workspace.Template,
			Phase:         workspace.Phase,
			Image:         workspace.Image,
			Persistent:    workspace.Persistent,
			BrowserVSCode: workspace.BrowserVSCode,
			Resources:     []topologyResource{},
		}
		if !workspace.CreatedAt.IsZero() {
			node.CreatedAt = workspace.CreatedAt.UTC().Format("2006-01-02T15:04:05Z")
		}
		if volumes, ok := byWorkspace[workspace.Name]; ok && len(volumes) > 0 {
			for _, volume := range volumes {
				node.Resources = append(node.Resources, topologyResource{
					Kind:         "PersistentVolumeClaim",
					Name:         volume.Name,
					Phase:        volume.Phase,
					StorageClass: volume.StorageClass,
					Capacity:     volume.Capacity,
					Requested:    volume.Requested,
					AccessModes:  volume.AccessModes,
					VolumeName:   volume.VolumeName,
				})
				user.VolumeCount++
				user.StorageBytes += volume.Bytes
				resp.Totals.Volumes++
				resp.Totals.StorageBytes += volume.Bytes
			}
		} else if !workspace.Persistent {
			node.Resources = append(node.Resources, topologyResource{
				Kind: "Ephemeral",
				Name: "no persistent volume",
			})
		}
		user.Workspaces = append(user.Workspaces, node)
		user.WorkspaceCount++
		resp.Totals.Workspaces++
	}

	resp.Users = make([]topologyUser, 0, len(users))
	for _, user := range users {
		sort.Slice(user.Workspaces, func(i, j int) bool {
			return user.Workspaces[i].Name < user.Workspaces[j].Name
		})
		resp.Users = append(resp.Users, *user)
	}
	sort.Slice(resp.Users, func(i, j int) bool {
		if resp.Users[i].Current != resp.Users[j].Current {
			return resp.Users[i].Current
		}
		return strings.ToLower(resp.Users[i].Identity) < strings.ToLower(resp.Users[j].Identity)
	})
	resp.Totals.Users = len(resp.Users)

	query := searchQuery(r)
	if query != "" {
		filtered := make([]topologyUser, 0, len(resp.Users))
		for _, user := range resp.Users {
			values := []string{user.Identity, user.Name}
			for _, workspace := range user.Workspaces {
				values = append(values, workspace.Name)
			}
			if matchesSearch(query, values...) {
				filtered = append(filtered, user)
			}
		}
		resp.Users = filtered
	}
	if !pagedRequest(r) {
		writeJSON(w, http.StatusOK, resp)
		return
	}
	page, perPage := pageRequest(r)
	items, info := paginate(resp.Users, page, perPage)
	resp.Users = items
	resp.Total = info.Total
	resp.Page = info.Page
	resp.PerPage = info.PerPage
	resp.Pages = info.Pages
	writeJSON(w, http.StatusOK, resp)
}

func ownerDisplayName(identity string) string {
	if local, _, ok := strings.Cut(identity, "@"); ok && local != "" {
		return local
	}
	return identity
}
