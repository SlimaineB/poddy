package region

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"

	"poddy/internal/k8s"
)

type Hub struct {
	registry *Registry
	clients  map[string]*Client

	// lastList keeps the most recent successful workspace list per region and
	// identity. It is used as a fallback when a region is temporarily
	// unreachable so a transient failure does not make workspaces disappear.
	lastList map[string]map[string][]k8s.Workspace
	// regionOf remembers where a workspace lives so detail and proxy requests
	// do not have to scan every region on each call.
	regionOf map[string]string
	mu       sync.RWMutex
}

func NewHub(registry *Registry) *Hub {
	hub := &Hub{
		registry: registry,
		clients:  map[string]*Client{},
		lastList: map[string]map[string][]k8s.Workspace{},
		regionOf: map[string]string{},
	}
	if registry == nil {
		return hub
	}
	for _, cfg := range registry.All() {
		hub.clients[cfg.Name] = NewClient(cfg)
	}
	return hub
}

func identityCacheKey(identity Identity) string {
	return fmt.Sprintf("%t|%s|%s", identity.Admin, identity.User.Username, identity.User.Subject)
}

func (h *Hub) rememberList(region, identityKey string, workspaces []k8s.Workspace) {
	if h == nil {
		return
	}
	snapshot := append([]k8s.Workspace(nil), workspaces...)
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.lastList == nil {
		h.lastList = map[string]map[string][]k8s.Workspace{}
	}
	byIdentity := h.lastList[region]
	if byIdentity == nil {
		byIdentity = map[string][]k8s.Workspace{}
		h.lastList[region] = byIdentity
	}
	byIdentity[identityKey] = snapshot
	if h.regionOf == nil {
		h.regionOf = map[string]string{}
	}
	for _, workspace := range workspaces {
		if workspace.Name != "" {
			h.regionOf[workspace.Name] = region
		}
	}
}

func (h *Hub) cachedList(region, identityKey string) []k8s.Workspace {
	if h == nil {
		return nil
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.lastList[region][identityKey]
}

func (h *Hub) rememberWorkspace(name, region string) {
	if h == nil || name == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.regionOf == nil {
		h.regionOf = map[string]string{}
	}
	h.regionOf[name] = region
}

func (h *Hub) cachedRegion(name string) (string, bool) {
	if h == nil {
		return "", false
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	region, ok := h.regionOf[name]
	return region, ok
}

func (h *Hub) forgetWorkspace(name string) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.regionOf, name)
}

func (h *Hub) Configs() []Config {
	if h == nil || h.registry == nil {
		return nil
	}
	return h.registry.All()
}

func (h *Hub) Default() (Config, bool) {
	if h == nil || h.registry == nil {
		return Config{}, false
	}
	return h.registry.Default()
}

func (h *Hub) Get(name string) (Config, bool) {
	if h == nil || h.registry == nil {
		return Config{}, false
	}
	return h.registry.Get(name)
}

func (h *Hub) Client(name string) *Client {
	if h == nil {
		return nil
	}
	return h.clients[name]
}

func (h *Hub) SetTunnel(d TunnelDialer) {
	if h == nil {
		return
	}
	for _, client := range h.clients {
		client.SetTunnel(d)
	}
}

func (h *Hub) List(ctx context.Context, identity Identity) ([]k8s.Workspace, []error) {
	if h == nil {
		return nil, nil
	}
	var (
		mu         sync.Mutex
		wg         sync.WaitGroup
		workspaces []k8s.Workspace
		problems   []error
	)
	identityKey := identityCacheKey(identity)
	for _, cfg := range h.Configs() {
		client := h.clients[cfg.Name]
		if client == nil {
			continue
		}
		wg.Add(1)
		go func(cfg Config, client *Client) {
			defer wg.Done()
			list, err := client.List(ctx, identity)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				problems = append(problems, fmt.Errorf("%s: %w", cfg.Name, err))
				// Keep the last known workspaces for this region instead of
				// dropping them from the list during a transient outage.
				workspaces = append(workspaces, h.cachedList(cfg.Name, identityKey)...)
				return
			}
			h.rememberList(cfg.Name, identityKey, list)
			workspaces = append(workspaces, list...)
		}(cfg, client)
	}
	wg.Wait()
	SortWorkspaces(workspaces)
	return workspaces, problems
}

func (h *Hub) ListRegion(ctx context.Context, region string, identity Identity) ([]k8s.Workspace, error) {
	cfg, ok := h.Get(region)
	if !ok {
		return nil, fmt.Errorf("unknown region %q", region)
	}
	client := h.Client(cfg.Name)
	if client == nil {
		return nil, fmt.Errorf("region %s is not configured", region)
	}
	return client.List(ctx, identity)
}

func (h *Hub) Volumes(ctx context.Context, identity Identity) ([]k8s.Volume, bool, []error) {
	if h == nil {
		return nil, false, nil
	}
	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		volumes  []k8s.Volume
		backup   bool
		problems []error
	)
	for _, cfg := range h.Configs() {
		client := h.clients[cfg.Name]
		if client == nil {
			continue
		}
		wg.Add(1)
		go func(cfg Config, client *Client) {
			defer wg.Done()
			response, err := client.VolumesResponse(ctx, identity)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				problems = append(problems, fmt.Errorf("%s: %w", cfg.Name, err))
				return
			}
			backup = backup || response.Backup
			volumes = append(volumes, response.Volumes...)
		}(cfg, client)
	}
	wg.Wait()
	sort.Slice(volumes, func(i, j int) bool {
		if volumes[i].Region == volumes[j].Region {
			if volumes[i].Workspace == volumes[j].Workspace {
				return volumes[i].Name < volumes[j].Name
			}
			return volumes[i].Workspace < volumes[j].Workspace
		}
		return volumes[i].Region < volumes[j].Region
	})
	return volumes, backup, problems
}

func (h *Hub) VolumesRegion(ctx context.Context, region string, identity Identity) ([]k8s.Volume, bool, error) {
	cfg, ok := h.Get(region)
	if !ok {
		return nil, false, fmt.Errorf("unknown region %q", region)
	}
	client := h.Client(cfg.Name)
	if client == nil {
		return nil, false, fmt.Errorf("region %s is not configured", region)
	}
	response, err := client.VolumesResponse(ctx, identity)
	return response.Volumes, response.Backup, err
}

func (h *Hub) FindVolume(ctx context.Context, name string, identity Identity) (Config, k8s.Volume, error) {
	if h == nil {
		return Config{}, k8s.Volume{}, errors.New("no regions configured")
	}
	var problems []error
	for _, cfg := range h.Configs() {
		client := h.clients[cfg.Name]
		if client == nil {
			continue
		}
		volumes, err := client.Volumes(ctx, identity)
		if err != nil {
			problems = append(problems, fmt.Errorf("%s: %w", cfg.Name, err))
			continue
		}
		for _, volume := range volumes {
			if volume.Name == name {
				return cfg, volume, nil
			}
		}
	}
	if len(problems) > 0 {
		return Config{}, k8s.Volume{}, errors.Join(problems...)
	}
	return Config{}, k8s.Volume{}, &HTTPError{Status: 404, Message: "volume not found"}
}

func (h *Hub) FindVolumeInRegion(ctx context.Context, region, name string, identity Identity) (Config, k8s.Volume, error) {
	if region == "" {
		return h.FindVolume(ctx, name, identity)
	}
	cfg, ok := h.Get(region)
	if !ok {
		return Config{}, k8s.Volume{}, &HTTPError{Status: 404, Message: "region not found"}
	}
	client := h.Client(cfg.Name)
	if client == nil {
		return Config{}, k8s.Volume{}, fmt.Errorf("region %s is not configured", region)
	}
	volumes, err := client.Volumes(ctx, identity)
	if err != nil {
		return Config{}, k8s.Volume{}, err
	}
	for _, volume := range volumes {
		if volume.Name == name {
			return cfg, volume, nil
		}
	}
	return Config{}, k8s.Volume{}, &HTTPError{Status: 404, Message: "volume not found"}
}

func (h *Hub) FindVolumeRegion(ctx context.Context, name string, identity Identity) (string, error) {
	cfg, _, err := h.FindVolume(ctx, name, identity)
	if err != nil {
		return "", err
	}
	return cfg.Name, nil
}

func (h *Hub) FindWorkspace(ctx context.Context, name string, identity Identity) (Config, WorkspaceInfo, error) {
	if h == nil {
		return Config{}, WorkspaceInfo{}, errors.New("no regions configured")
	}
	if regionName, ok := h.cachedRegion(name); ok {
		if cfg, ok := h.Get(regionName); ok {
			if client := h.Client(cfg.Name); client != nil {
				info, err := client.GetWorkspace(ctx, name, identity)
				if err == nil {
					return cfg, info, nil
				}
				if IsNotFound(err) {
					// Only a real 404 invalidates the hint. A 403 means the
					// caller cannot see this workspace; keep the hint for the
					// owner and fall through to the normal scan.
					var httpErr *HTTPError
					if errors.As(err, &httpErr) && httpErr.Status == http.StatusNotFound {
						h.forgetWorkspace(name)
					}
				} else {
					return Config{}, WorkspaceInfo{}, fmt.Errorf("%s: %w", cfg.Name, err)
				}
			}
		}
	}
	var problems []error
	for _, cfg := range h.Configs() {
		client := h.clients[cfg.Name]
		if client == nil {
			continue
		}
		info, err := client.GetWorkspace(ctx, name, identity)
		if err == nil {
			h.rememberWorkspace(name, cfg.Name)
			return cfg, info, nil
		}
		if IsNotFound(err) {
			continue
		}
		problems = append(problems, fmt.Errorf("%s: %w", cfg.Name, err))
	}
	if len(problems) > 0 {
		return Config{}, WorkspaceInfo{}, errors.Join(problems...)
	}
	return Config{}, WorkspaceInfo{}, &HTTPError{Status: 404, Message: "workspace not found"}
}

func (h *Hub) Details(ctx context.Context, name string, identity Identity) (map[string]any, error) {
	if regionName, ok := h.cachedRegion(name); ok {
		if cfg, ok := h.Get(regionName); ok {
			if client := h.Client(cfg.Name); client != nil {
				details, err := client.Details(ctx, name, identity)
				if err == nil {
					return details, nil
				}
				if !IsNotFound(err) {
					return nil, fmt.Errorf("%s: %w", cfg.Name, err)
				}
			}
		}
	}
	cfg, _, err := h.FindWorkspace(ctx, name, identity)
	if err != nil {
		return nil, err
	}
	client := h.Client(cfg.Name)
	if client == nil {
		return nil, fmt.Errorf("region %s is not configured", cfg.Name)
	}
	return client.Details(ctx, name, identity)
}

func (h *Hub) Create(ctx context.Context, regionName string, req k8s.CreateRequest, identity Identity) (k8s.Workspace, error) {
	cfg, ok := h.Get(regionName)
	if !ok {
		return k8s.Workspace{}, fmt.Errorf("unknown region %q", regionName)
	}
	client := h.Client(cfg.Name)
	if client == nil {
		return k8s.Workspace{}, fmt.Errorf("region %s is not configured", cfg.Name)
	}
	_, _, err := h.FindWorkspace(ctx, req.Name, identity)
	if err == nil {
		return k8s.Workspace{}, fmt.Errorf("workspace %q already exists", req.Name)
	}
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr.Status != 404 {
		return k8s.Workspace{}, err
	}
	created, err := client.Create(ctx, req, identity)
	if err == nil {
		h.rememberWorkspace(req.Name, cfg.Name)
	}
	return created, err
}

func (h *Hub) Topology(ctx context.Context, identity Identity) (Topology, []error) {
	if h == nil {
		return Topology{}, nil
	}
	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		combined Topology
		problems []error
	)
	users := map[string]*TopologyUser{}
	for _, cfg := range h.Configs() {
		client := h.clients[cfg.Name]
		if client == nil {
			continue
		}
		wg.Add(1)
		go func(cfg Config, client *Client) {
			defer wg.Done()
			topology, err := client.Topology(ctx, identity)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				problems = append(problems, fmt.Errorf("%s: %w", cfg.Name, err))
				return
			}
			combined.Totals.Volumes += topology.Totals.Volumes
			combined.Totals.StorageBytes += topology.Totals.StorageBytes
			for _, user := range topology.Users {
				target := users[user.Identity]
				if target == nil {
					target = &TopologyUser{
						Identity:   user.Identity,
						Name:       user.Name,
						Current:    user.Current,
						Workspaces: []TopologyWorkspace{},
					}
					users[user.Identity] = target
				}
				target.WorkspaceCount += user.WorkspaceCount
				target.VolumeCount += user.VolumeCount
				target.StorageBytes += user.StorageBytes
				target.Workspaces = append(target.Workspaces, user.Workspaces...)
			}
		}(cfg, client)
	}
	wg.Wait()
	combined.Users = make([]TopologyUser, 0, len(users))
	for _, user := range users {
		sort.Slice(user.Workspaces, func(i, j int) bool {
			return user.Workspaces[i].Name < user.Workspaces[j].Name
		})
		combined.Totals.Workspaces += user.WorkspaceCount
		combined.Users = append(combined.Users, *user)
	}
	sort.Slice(combined.Users, func(i, j int) bool {
		if combined.Users[i].Current != combined.Users[j].Current {
			return combined.Users[i].Current
		}
		return strings.ToLower(combined.Users[i].Identity) < strings.ToLower(combined.Users[j].Identity)
	})
	combined.Totals.Users = len(combined.Users)
	return combined, problems
}

func (h *Hub) TopologyRegion(ctx context.Context, region string, identity Identity) (Topology, error) {
	cfg, ok := h.Get(region)
	if !ok {
		return Topology{}, fmt.Errorf("unknown region %q", region)
	}
	client := h.Client(cfg.Name)
	if client == nil {
		return Topology{}, fmt.Errorf("region %s is not configured", region)
	}
	return client.Topology(ctx, identity)
}

type Summary struct {
	Name        string  `json:"name"`
	DisplayName string  `json:"displayName,omitempty"`
	Default     bool    `json:"default,omitempty"`
	SSHHost     string  `json:"sshHost,omitempty"`
	SSHPort     string  `json:"sshPort,omitempty"`
	Lat         float64 `json:"lat,omitempty"`
	Lon         float64 `json:"lon,omitempty"`
	Available   bool    `json:"available"`
	Users       int     `json:"users"`
	Workspaces  int     `json:"workspaces"`
	Running     int     `json:"running"`
	Stopped     int     `json:"stopped"`
}

func (h *Hub) Summaries(ctx context.Context, identity Identity) []Summary {
	if h == nil {
		return []Summary{}
	}
	configs := h.Configs()
	summaries := make([]Summary, len(configs))
	var wg sync.WaitGroup
	for i, cfg := range configs {
		summaries[i] = Summary{
			Name:        cfg.Name,
			DisplayName: cfg.DisplayName,
			Default:     cfg.Default,
			SSHHost:     cfg.SSHHost,
			SSHPort:     cfg.SSHPort,
			Lat:         cfg.Lat,
			Lon:         cfg.Lon,
		}
		client := h.Client(cfg.Name)
		if client == nil {
			continue
		}
		wg.Add(1)
		go func(index int, client *Client) {
			defer wg.Done()
			workspaces, err := client.List(ctx, identity)
			if err != nil {
				return
			}
			summaries[index].Available = true
			summaries[index].Workspaces = len(workspaces)
			owners := map[string]struct{}{}
			for _, workspace := range workspaces {
				if workspace.Owner != "" {
					owners[workspace.Owner] = struct{}{}
				}
				switch workspace.Phase {
				case "Running":
					summaries[index].Running++
				case "Stopped":
					summaries[index].Stopped++
				}
			}
			summaries[index].Users = len(owners)
		}(i, client)
	}
	wg.Wait()
	return summaries
}

func (h *Hub) SyncSSHKeys(ctx context.Context, owner string, keys []string, identity Identity) []error {
	if h == nil {
		return nil
	}
	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		problems []error
	)
	for _, cfg := range h.Configs() {
		client := h.clients[cfg.Name]
		if client == nil || !client.available() {
			continue
		}
		wg.Add(1)
		go func(cfg Config, client *Client) {
			defer wg.Done()
			if err := client.ReplaceSSHKeys(ctx, owner, keys, identity); err != nil {
				mu.Lock()
				problems = append(problems, fmt.Errorf("%s: %w", cfg.Name, err))
				mu.Unlock()
				log.Printf("region %s: sync SSH keys for %s: %v", cfg.Name, owner, err)
			}
		}(cfg, client)
	}
	wg.Wait()
	return problems
}
