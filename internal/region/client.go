package region

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sort"
	"strings"
	"time"

	"poddy/internal/k8s"
	"poddy/internal/templates"
)

type HTTPError struct {
	Status  int
	Message string
}

func (e *HTTPError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("region returned HTTP %d", e.Status)
	}
	return fmt.Sprintf("region returned HTTP %d: %s", e.Status, e.Message)
}

type Client struct {
	cfg  Config
	http *http.Client
}

type WorkspaceInfo struct {
	Name          string    `json:"name"`
	Template      string    `json:"template"`
	Phase         string    `json:"phase"`
	Image         string    `json:"image"`
	Owner         string    `json:"owner"`
	Persistent    bool      `json:"persistent"`
	BrowserVSCode bool      `json:"browserVSCode"`
	CreatedAt     time.Time `json:"createdAt"`
}

type TopologyResource struct {
	Kind         string   `json:"kind"`
	Name         string   `json:"name"`
	Phase        string   `json:"phase,omitempty"`
	StorageClass string   `json:"storageClass,omitempty"`
	Capacity     string   `json:"capacity,omitempty"`
	Requested    string   `json:"requested,omitempty"`
	AccessModes  []string `json:"accessModes,omitempty"`
	VolumeName   string   `json:"volumeName,omitempty"`
}

type TopologyWorkspace struct {
	Name          string             `json:"name"`
	Template      string             `json:"template,omitempty"`
	Phase         string             `json:"phase,omitempty"`
	Image         string             `json:"image,omitempty"`
	Persistent    bool               `json:"persistent"`
	BrowserVSCode bool               `json:"browserVSCode,omitempty"`
	CreatedAt     string             `json:"createdAt,omitempty"`
	Region        string             `json:"region,omitempty"`
	Resources     []TopologyResource `json:"resources"`
}

type TopologyUser struct {
	Identity       string              `json:"identity"`
	Name           string              `json:"name"`
	Current        bool                `json:"current"`
	WorkspaceCount int                 `json:"workspaceCount"`
	VolumeCount    int                 `json:"volumeCount"`
	StorageBytes   int64               `json:"storageBytes"`
	Workspaces     []TopologyWorkspace `json:"workspaces"`
}

type TopologyTotals struct {
	Users        int   `json:"users"`
	Workspaces   int   `json:"workspaces"`
	Volumes      int   `json:"volumes"`
	StorageBytes int64 `json:"storageBytes"`
}

type Topology struct {
	Totals  TopologyTotals `json:"totals"`
	Users   []TopologyUser `json:"users"`
	Total   int            `json:"total,omitempty"`
	Page    int            `json:"page,omitempty"`
	PerPage int            `json:"perPage,omitempty"`
	Pages   int            `json:"pages,omitempty"`
}

func NewClient(cfg Config) *Client {
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   32,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
	}
	return &Client{
		cfg: cfg,
		http: &http.Client{
			Transport: transport,
		},
	}
}

func (c *Client) Config() Config { return c.cfg }

func (c *Client) List(ctx context.Context, identity Identity) ([]k8s.Workspace, error) {
	var out []k8s.Workspace
	if err := c.doJSON(ctx, http.MethodGet, "/api/workspaces", identity, nil, &out); err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Region = c.cfg.Name
	}
	return out, nil
}

func (c *Client) GetWorkspace(ctx context.Context, name string, identity Identity) (WorkspaceInfo, error) {
	var out map[string]any
	if err := c.doJSON(ctx, http.MethodGet, "/api/workspaces/"+url.PathEscape(name), identity, nil, &out); err != nil {
		return WorkspaceInfo{}, err
	}
	return infoFromMap(out), nil
}

func (c *Client) Details(ctx context.Context, name string, identity Identity) (map[string]any, error) {
	var out map[string]any
	if err := c.doJSON(ctx, http.MethodGet, "/api/workspaces/"+url.PathEscape(name), identity, nil, &out); err != nil {
		return nil, err
	}
	out["region"] = c.cfg.Name
	out["regionDisplayName"] = c.cfg.DisplayName
	return out, nil
}

func (c *Client) Create(ctx context.Context, req k8s.CreateRequest, identity Identity) (k8s.Workspace, error) {
	var out k8s.Workspace
	if err := c.doJSON(ctx, http.MethodPost, "/api/workspaces", identity, req, &out); err != nil {
		return k8s.Workspace{}, err
	}
	out.Region = c.cfg.Name
	return out, nil
}

type VolumesResponse struct {
	Volumes []k8s.Volume
	Backup  bool
}

func (c *Client) VolumesResponse(ctx context.Context, identity Identity) (VolumesResponse, error) {
	var response struct {
		Volumes []k8s.Volume `json:"volumes"`
		Backup  bool         `json:"backup"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/api/volumes", identity, nil, &response); err != nil {
		return VolumesResponse{}, err
	}
	for i := range response.Volumes {
		response.Volumes[i].Region = c.cfg.Name
	}
	return VolumesResponse{Volumes: response.Volumes, Backup: response.Backup}, nil
}

func (c *Client) Volumes(ctx context.Context, identity Identity) ([]k8s.Volume, error) {
	response, err := c.VolumesResponse(ctx, identity)
	if err != nil {
		return nil, err
	}
	return response.Volumes, nil
}

func (c *Client) Topology(ctx context.Context, identity Identity) (Topology, error) {
	var out Topology
	if err := c.doJSON(ctx, http.MethodGet, "/api/topology", identity, nil, &out); err != nil {
		return Topology{}, err
	}
	for i := range out.Users {
		for j := range out.Users[i].Workspaces {
			out.Users[i].Workspaces[j].Region = c.cfg.Name
		}
	}
	return out, nil
}

type ExternalAuthRequest struct {
	Owner        string                   `json:"owner"`
	Workspace    string                   `json:"workspace"`
	PortalURL    string                   `json:"portalUrl,omitempty"`
	Requirements []templates.ExternalAuth `json:"requirements"`
}

type ExternalAuthResponse struct {
	Env   map[string]string `json:"env"`
	Token string            `json:"token,omitempty"`
}

func (c *Client) FetchExternalAuth(ctx context.Context, req ExternalAuthRequest, identity Identity) (ExternalAuthResponse, error) {
	var out ExternalAuthResponse
	err := c.doJSON(ctx, http.MethodPost, "/api/internal/region/external-auth", identity, req, &out)
	return out, err
}

func (c *Client) ReplaceSSHKeys(ctx context.Context, owner string, keys []string, identity Identity) error {
	body := struct {
		Keys []string `json:"keys"`
	}{Keys: keys}
	return c.doJSON(ctx, http.MethodPut, "/api/internal/ssh-keys/"+url.PathEscape(owner), identity, body, nil)
}

func (c *Client) doJSON(ctx context.Context, method, path string, identity Identity, body, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.cfg.URL+path, reader)
	if err != nil {
		return err
	}
	token, err := SignIdentity(c.cfg.Key, identity)
	if err != nil {
		return err
	}
	req.Header.Set(IdentityHeader, token)
	req.Header.Set(RegionNameHeader, c.cfg.Name)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if out == nil {
			io.Copy(io.Discard, resp.Body)
			return nil
		}
		return json.NewDecoder(resp.Body).Decode(out)
	}
	message, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return &HTTPError{Status: resp.StatusCode, Message: strings.TrimSpace(string(message))}
}

func (c *Client) Proxy(w http.ResponseWriter, r *http.Request, identity Identity) {
	target, err := url.Parse(c.cfg.URL)
	if err != nil {
		http.Error(w, "invalid region URL", http.StatusBadGateway)
		return
	}
	token, err := SignIdentity(c.cfg.Key, identity)
	if err != nil {
		http.Error(w, "cannot sign region identity", http.StatusInternalServerError)
		return
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.FlushInterval = 100 * time.Millisecond
	originalHost := r.Host
	originalProto := "http"
	if r.TLS != nil {
		originalProto = "https"
	}
	if forwarded := r.Header.Get("X-Forwarded-Proto"); forwarded != "" {
		originalProto = strings.TrimSpace(strings.Split(forwarded, ",")[0])
	}
	director := proxy.Director
	proxy.Director = func(req *http.Request) {
		director(req)
		req.Header.Set(IdentityHeader, token)
		req.Header.Del("Authorization")
		req.Header.Set("X-Forwarded-Host", originalHost)
		req.Header.Set("X-Forwarded-Proto", originalProto)
		req.Host = originalHost
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		log.Printf("region %s proxy %s: %v", c.cfg.Name, r.URL.Path, err)
		http.Error(w, "region unavailable", http.StatusBadGateway)
	}
	proxy.ServeHTTP(w, r)
}

func infoFromMap(value map[string]any) WorkspaceInfo {
	var out WorkspaceInfo
	out.Name, _ = value["name"].(string)
	out.Template, _ = value["template"].(string)
	out.Phase, _ = value["phase"].(string)
	out.Image, _ = value["image"].(string)
	out.Owner, _ = value["owner"].(string)
	out.Persistent, _ = value["persistent"].(bool)
	out.BrowserVSCode, _ = value["browserVSCode"].(bool)
	if raw, ok := value["createdAt"].(string); ok {
		out.CreatedAt, _ = time.Parse(time.RFC3339, raw)
	}
	return out
}

func SortWorkspaces(list []k8s.Workspace) {
	sort.Slice(list, func(i, j int) bool {
		return strings.ToLower(list[i].Name) < strings.ToLower(list[j].Name)
	})
}

func IsNotFound(err error) bool {
	var httpErr *HTTPError
	return errors.As(err, &httpErr) && (httpErr.Status == http.StatusNotFound || httpErr.Status == http.StatusForbidden)
}
