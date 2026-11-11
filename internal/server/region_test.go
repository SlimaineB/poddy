package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"poddy/internal/auth"
	"poddy/internal/k8s"
	"poddy/internal/region"
)

func newHubServer(t *testing.T, regionURL string) *Server {
	t.Helper()
	registry, err := region.Parse([]byte("regions:\n- name: eu\n  url: " + regionURL + "\n  key: shared\n  default: true\n"))
	if err != nil {
		t.Fatal(err)
	}
	s := newTestServer("")
	s.Regions = region.NewHub(registry)
	return s
}

func TestHubCreateListAndDetails(t *testing.T) {
	regionServer := newTestServer("")
	regionServer.RegionMode = true
	regionServer.RegionKey = "shared"
	regionServer.RegionName = "eu"
	backend := httptest.NewServer(regionServer.Handler())
	defer backend.Close()

	hub := newHubServer(t, backend.URL)

	rec := request(t, hub, http.MethodPost, "/api/workspaces", map[string]any{"name": "app", "region": "eu"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var created k8s.Workspace
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Name != "app" || created.Region != "eu" {
		t.Fatalf("created = %+v", created)
	}

	rec = request(t, hub, http.MethodGet, "/api/workspaces", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var list []k8s.Workspace
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Region != "eu" {
		t.Fatalf("list = %+v", list)
	}

	rec = request(t, hub, http.MethodGet, "/api/workspaces/app", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("details status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var details map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &details); err != nil {
		t.Fatal(err)
	}
	if details["region"] != "eu" || details["owner"] == "" {
		t.Fatalf("details = %+v", details)
	}

	rec = request(t, hub, http.MethodGet, "/api/workspaces/app/manifests", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("manifests status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var manifests map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &manifests); err != nil {
		t.Fatal(err)
	}
	if manifests["podSource"] != "live" || manifests["pod"] == "" {
		t.Fatalf("manifests = %+v", manifests)
	}
}

func TestRegionModeRejectsUnsignedRequests(t *testing.T) {
	regionServer := newTestServer("")
	regionServer.RegionMode = true
	regionServer.RegionKey = "shared"

	rec := request(t, regionServer, http.MethodGet, "/api/workspaces", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestRegionSSHKeySyncRequiresRegionAdmin(t *testing.T) {
	regionServer := newTestServer("")
	regionServer.RegionMode = true
	regionServer.RegionKey = "shared"
	regionServer.UserKeys = nil
	regionServer.SSHKeys = nil

	rec := request(t, regionServer, http.MethodPut, "/api/internal/ssh-keys/alice@example.com", map[string]any{"keys": []string{}})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestHubRegionFilterAndSummaries(t *testing.T) {
	newRegion := func(name string) *httptest.Server {
		server := newTestServer("")
		server.RegionMode = true
		server.RegionKey = "shared"
		server.RegionName = name
		backend := httptest.NewServer(server.Handler())
		t.Cleanup(backend.Close)
		return backend
	}
	eu := newRegion("eu")
	us := newRegion("us")

	registry, err := region.Parse([]byte(
		"regions:\n" +
			"- name: eu\n  displayName: Europe\n  url: " + eu.URL + "\n  key: shared\n  default: true\n" +
			"- name: us\n  displayName: United States\n  url: " + us.URL + "\n  key: shared\n",
	))
	if err != nil {
		t.Fatal(err)
	}
	hub := newTestServer("")
	hub.Regions = region.NewHub(registry)

	for _, req := range []map[string]any{
		{"name": "app-eu", "region": "eu"},
		{"name": "app-us", "region": "us"},
	} {
		rec := request(t, hub, http.MethodPost, "/api/workspaces", req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %v status = %d, body = %s", req, rec.Code, rec.Body.String())
		}
	}

	rec := request(t, hub, http.MethodGet, "/api/workspaces?region=eu", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("filtered list status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var list []k8s.Workspace
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Name != "app-eu" || list[0].Region != "eu" {
		t.Fatalf("filtered list = %+v", list)
	}

	rec = request(t, hub, http.MethodGet, "/api/topology?region=us", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("filtered topology status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var topology region.Topology
	if err := json.Unmarshal(rec.Body.Bytes(), &topology); err != nil {
		t.Fatal(err)
	}
	if topology.Totals.Workspaces != 1 || len(topology.Users) != 1 || len(topology.Users[0].Workspaces) != 1 {
		t.Fatalf("filtered topology = %+v", topology)
	}
	if topology.Users[0].Workspaces[0].Region != "us" {
		t.Fatalf("topology region = %q", topology.Users[0].Workspaces[0].Region)
	}

	rec = request(t, hub, http.MethodGet, "/api/regions", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("region summaries status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var summaries []region.Summary
	if err := json.Unmarshal(rec.Body.Bytes(), &summaries); err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 2 {
		t.Fatalf("summaries = %+v", summaries)
	}
	for _, summary := range summaries {
		if !summary.Available || summary.Workspaces != 1 || summary.Users != 1 {
			t.Fatalf("summary = %+v", summary)
		}
	}
}

func TestHubProxiesWorkspacePorts(t *testing.T) {
	regionServer := newTestServer("")
	regionServer.RegionMode = true
	regionServer.RegionKey = "shared"
	regionServer.RegionName = "eu"
	backend := httptest.NewServer(regionServer.Handler())
	defer backend.Close()

	hub := newHubServer(t, backend.URL)
	rec := request(t, hub, http.MethodPost, "/api/workspaces", map[string]any{"name": "app", "region": "eu"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", rec.Code, rec.Body.String())
	}

	rec = request(t, hub, http.MethodGet, "/api/workspaces/app/ports", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("ports status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	ports, ok := payload["ports"].([]any)
	if !ok || len(ports) != 0 {
		t.Fatalf("ports = %#v", payload["ports"])
	}
}

func TestHubProxiesShareLink(t *testing.T) {
	regionServer := newTestServer("")
	regionServer.RegionMode = true
	regionServer.RegionKey = "shared"
	regionServer.RegionName = "eu"
	backend := httptest.NewServer(regionServer.Handler())
	defer backend.Close()

	registry, err := region.Parse([]byte("regions:\n- name: eu\n  url: " + backend.URL + "\n  key: shared\n  default: true\n"))
	if err != nil {
		t.Fatal(err)
	}
	hub := newRBACServer(t, rbacTestConfig())
	hub.Regions = region.NewHub(registry)
	admin := sessionFor(t, hub, auth.User{Username: "admin", Email: "admin@example.com"})

	rec := requestAs(t, hub, admin, http.MethodPost, "/api/workspaces", map[string]any{"name": "app", "region": "eu"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var ws struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &ws); err != nil {
		t.Fatal(err)
	}

	rec = requestAs(t, hub, admin, http.MethodPut, "/api/workspaces/"+ws.Name+"/ports/4000/share", map[string]string{"access": "public"})
	if rec.Code != http.StatusOK {
		t.Fatalf("share status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var created struct {
		Share struct {
			URL string `json:"url"`
		} `json:"share"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Share.URL == "" || created.Share.URL[:7] != "/share/" {
		t.Fatalf("share url = %q", created.Share.URL)
	}

	rec = request(t, hub, http.MethodGet, created.Share.URL, nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("share link status = %d, body = %s", rec.Code, rec.Body.String())
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != shareCookie {
		t.Fatalf("cookies = %+v", cookies)
	}
	target := "/api/workspaces/" + ws.Name + "/ports/4000/"
	if got := rec.Header().Get("Location"); got != target {
		t.Fatalf("location = %q", got)
	}

	rec = requestAs(t, hub, cookies[0], http.MethodGet, target, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("target status = %d, body = %s", rec.Code, rec.Body.String())
	}
}
