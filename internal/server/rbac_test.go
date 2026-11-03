package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"poddy/internal/auth"
	"poddy/internal/rbac"
)

func rbacTestConfig() rbac.Config {
	return rbac.Config{
		Admins: []string{"admin@example.com"},
		Templates: map[string]rbac.Access{
			"go": {Owners: []string{"dev@example.com"}, Users: []string{"alice@example.com"}},
		},
	}
}

func newRBACServer(t *testing.T, cfg rbac.Config) *Server {
	t.Helper()
	s, _ := newOIDCTestServer(t)
	s.RBAC = rbac.NewStore(s.Workspaces.Client, "poddy", "")
	if err := s.RBAC.Save(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	return s
}

func sessionFor(t *testing.T, s *Server, user auth.User) *http.Cookie {
	t.Helper()
	value, err := s.OIDC.SignSession(user)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Cookie{Name: sessionCookie, Value: value}
}

func requestAs(t *testing.T, s *Server, cookie *http.Cookie, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(data)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

func templatesByName(t *testing.T, rec *httptest.ResponseRecorder) map[string]templateInfo {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var list []templateInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	out := map[string]templateInfo{}
	for _, template := range list {
		out[template.Name] = template
	}
	return out
}

func TestRBACTemplateVisibility(t *testing.T) {
	s := newRBACServer(t, rbacTestConfig())
	admin := sessionFor(t, s, auth.User{Username: "admin", Email: "admin@example.com"})
	dev := sessionFor(t, s, auth.User{Username: "dev", Email: "dev@example.com"})
	alice := sessionFor(t, s, auth.User{Username: "alice", Email: "alice@example.com"})
	bob := sessionFor(t, s, auth.User{Username: "bob", Email: "bob@example.com"})

	got := templatesByName(t, requestAs(t, s, admin, http.MethodGet, "/api/templates", nil))
	if len(got) != 2 || !got["go"].CanManage || !got["base"].CanManage {
		t.Fatalf("admin templates = %+v", got)
	}

	got = templatesByName(t, requestAs(t, s, dev, http.MethodGet, "/api/templates", nil))
	if len(got) != 2 {
		t.Fatalf("owner templates = %+v", got)
	}
	if !got["go"].Owned || !got["go"].CanManage {
		t.Fatalf("owner flags = %+v", got)
	}
	if got["base"].CanManage {
		t.Fatalf("base should not be manageable: %+v", got["base"])
	}

	got = templatesByName(t, requestAs(t, s, alice, http.MethodGet, "/api/templates", nil))
	if len(got) != 2 || got["go"].Name != "go" || got["base"].Name != "base" {
		t.Fatalf("alice templates = %+v", got)
	}
	if got["go"].CanManage || got["go"].Owned {
		t.Fatalf("alice should not manage go: %+v", got["go"])
	}

	got = templatesByName(t, requestAs(t, s, bob, http.MethodGet, "/api/templates", nil))
	if len(got) != 1 || got["base"].Name != "base" {
		t.Fatalf("bob templates = %+v", got)
	}
}

func TestRBACWorkspaceOwnership(t *testing.T) {
	s := newRBACServer(t, rbacTestConfig())
	admin := sessionFor(t, s, auth.User{Username: "admin", Email: "admin@example.com"})
	dev := sessionFor(t, s, auth.User{Username: "dev", Email: "dev@example.com"})
	alice := sessionFor(t, s, auth.User{Username: "alice", Email: "alice@example.com"})
	bob := sessionFor(t, s, auth.User{Username: "bob", Email: "bob@example.com"})

	rec := requestAs(t, s, alice, http.MethodPost, "/api/workspaces", map[string]any{"name": "alice-ws", "template": "base"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("alice create = %d, body = %s", rec.Code, rec.Body.String())
	}
	var ws map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &ws); err != nil {
		t.Fatal(err)
	}
	if ws["owner"] != "alice@example.com" {
		t.Fatalf("owner = %+v", ws)
	}

	rec = requestAs(t, s, bob, http.MethodPost, "/api/workspaces", map[string]any{"name": "bob-ws", "template": "base"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("bob create = %d, body = %s", rec.Code, rec.Body.String())
	}

	count := func(cookie *http.Cookie) int {
		rec := requestAs(t, s, cookie, http.MethodGet, "/api/workspaces", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("list = %d, body = %s", rec.Code, rec.Body.String())
		}
		var list []map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
			t.Fatal(err)
		}
		return len(list)
	}
	if got := count(alice); got != 1 {
		t.Fatalf("alice count = %d", got)
	}
	if got := count(bob); got != 1 {
		t.Fatalf("bob count = %d", got)
	}
	if got := count(dev); got != 0 {
		t.Fatalf("owner should only see own workspaces, got %d", got)
	}
	if got := count(admin); got != 2 {
		t.Fatalf("admin count = %d", got)
	}

	if rec := requestAs(t, s, bob, http.MethodDelete, "/api/workspaces/alice-ws", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("bob delete alice = %d", rec.Code)
	}
	if rec := requestAs(t, s, alice, http.MethodDelete, "/api/workspaces/alice-ws", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("alice delete own = %d", rec.Code)
	}
	if rec := requestAs(t, s, admin, http.MethodDelete, "/api/workspaces/bob-ws", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("admin delete bob = %d", rec.Code)
	}
}

func TestRBACTemplateRestrictionsOnCreate(t *testing.T) {
	s := newRBACServer(t, rbacTestConfig())
	admin := sessionFor(t, s, auth.User{Username: "admin", Email: "admin@example.com"})
	dev := sessionFor(t, s, auth.User{Username: "dev", Email: "dev@example.com"})
	alice := sessionFor(t, s, auth.User{Username: "alice", Email: "alice@example.com"})
	bob := sessionFor(t, s, auth.User{Username: "bob", Email: "bob@example.com"})

	create := func(cookie *http.Cookie, name, template string) int {
		rec := requestAs(t, s, cookie, http.MethodPost, "/api/workspaces", map[string]any{"name": name, "template": template})
		return rec.Code
	}

	if code := create(alice, "alice-go", "go"); code != http.StatusCreated {
		t.Fatalf("assigned user go = %d", code)
	}
	if code := create(bob, "bob-go", "go"); code != http.StatusForbidden {
		t.Fatalf("unassigned user go = %d", code)
	}
	if code := create(dev, "dev-go", "go"); code != http.StatusCreated {
		t.Fatalf("owner go = %d", code)
	}
	if code := create(admin, "admin-go", "go"); code != http.StatusCreated {
		t.Fatalf("admin go = %d", code)
	}
}

func TestRBACTemplateAccessAPI(t *testing.T) {
	s := newRBACServer(t, rbacTestConfig())
	admin := sessionFor(t, s, auth.User{Username: "admin", Email: "admin@example.com"})
	dev := sessionFor(t, s, auth.User{Username: "dev", Email: "dev@example.com"})
	alice := sessionFor(t, s, auth.User{Username: "alice", Email: "alice@example.com"})
	bob := sessionFor(t, s, auth.User{Username: "bob", Email: "bob@example.com"})

	if rec := requestAs(t, s, alice, http.MethodGet, "/api/templates/go/access", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("alice access = %d", rec.Code)
	}
	rec := requestAs(t, s, dev, http.MethodGet, "/api/templates/go/access", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("dev access = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["canEditOwners"] != false {
		t.Fatalf("owner canEditOwners = %+v", body)
	}

	rec = requestAs(t, s, dev, http.MethodPut, "/api/templates/go/access", map[string]any{
		"owners": []string{"evil@example.com"},
		"users":  []string{"bob@example.com"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("dev update = %d, body = %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	owners := toStrings(body["owners"])
	if len(owners) != 1 || owners[0] != "dev@example.com" {
		t.Fatalf("owner changed by non-admin: %+v", owners)
	}

	got := templatesByName(t, requestAs(t, s, bob, http.MethodGet, "/api/templates", nil))
	if _, ok := got["go"]; !ok {
		t.Fatalf("bob should see go after assignment: %+v", got)
	}
	got = templatesByName(t, requestAs(t, s, alice, http.MethodGet, "/api/templates", nil))
	if _, ok := got["go"]; ok {
		t.Fatalf("alice should lose go after reassignment: %+v", got)
	}

	rec = requestAs(t, s, admin, http.MethodPut, "/api/templates/go/access", map[string]any{
		"owners": []string{"admin@example.com"},
		"users":  []string{"alice@example.com"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("admin update = %d, body = %s", rec.Code, rec.Body.String())
	}

	rec = requestAs(t, s, admin, http.MethodPut, "/api/templates/go/access", map[string]any{
		"owners": []string{},
		"users":  []string{},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("admin clear = %d, body = %s", rec.Code, rec.Body.String())
	}
	cfg, err := s.RBAC.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Templates["go"]; ok {
		t.Fatalf("go should be public after clearing access: %+v", cfg.Templates)
	}
	got = templatesByName(t, requestAs(t, s, alice, http.MethodGet, "/api/templates", nil))
	if _, ok := got["go"]; !ok {
		t.Fatalf("alice should see public go: %+v", got)
	}
}

func toStrings(value any) []string {
	items, _ := value.([]any)
	out := make([]string, 0, len(items))
	for _, item := range items {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func TestRBACTeams(t *testing.T) {
	s := newRBACServer(t, rbac.Config{
		Admins: []string{"admin@example.com"},
		Teams: map[string]rbac.Team{
			"backend": {Members: []string{"alice@example.com"}},
		},
		Templates: map[string]rbac.Access{
			"go": {Owners: []string{"dev@example.com"}, Teams: []string{"backend"}},
		},
	})
	admin := sessionFor(t, s, auth.User{Username: "admin", Email: "admin@example.com"})
	dev := sessionFor(t, s, auth.User{Username: "dev", Email: "dev@example.com"})
	alice := sessionFor(t, s, auth.User{Username: "alice", Email: "alice@example.com"})
	bob := sessionFor(t, s, auth.User{Username: "bob", Email: "bob@example.com"})

	got := templatesByName(t, requestAs(t, s, alice, http.MethodGet, "/api/templates", nil))
	if _, ok := got["go"]; !ok {
		t.Fatalf("team member should see go: %+v", got)
	}
	got = templatesByName(t, requestAs(t, s, bob, http.MethodGet, "/api/templates", nil))
	if _, ok := got["go"]; ok {
		t.Fatalf("non-member should not see go: %+v", got)
	}

	rec := requestAs(t, s, dev, http.MethodGet, "/api/templates/go/access", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("dev access = %d, body = %s", rec.Code, rec.Body.String())
	}
	var access map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &access); err != nil {
		t.Fatal(err)
	}
	if teams := toStrings(access["teams"]); len(teams) != 1 || teams[0] != "backend" {
		t.Fatalf("access teams = %+v", access)
	}

	if rec := requestAs(t, s, alice, http.MethodGet, "/api/teams", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("alice teams = %d", rec.Code)
	}
	rec = requestAs(t, s, dev, http.MethodGet, "/api/teams", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("dev teams = %d, body = %s", rec.Code, rec.Body.String())
	}
	var teams []teamInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &teams); err != nil {
		t.Fatal(err)
	}
	if len(teams) != 1 || teams[0].Name != "backend" || teams[0].MemberCount != 1 || teams[0].Members != nil {
		t.Fatalf("owner teams = %+v", teams)
	}
	if rec := requestAs(t, s, dev, http.MethodPut, "/api/teams/backend", map[string]any{"members": []string{"alice@example.com"}}); rec.Code != http.StatusForbidden {
		t.Fatalf("owner update team = %d", rec.Code)
	}

	rec = requestAs(t, s, admin, http.MethodGet, "/api/teams", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin teams = %d", rec.Code)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &teams); err != nil {
		t.Fatal(err)
	}
	if len(teams) != 1 || len(teams[0].Members) != 1 || teams[0].Members[0] != "alice@example.com" {
		t.Fatalf("admin teams = %+v", teams)
	}

	rec = requestAs(t, s, admin, http.MethodPut, "/api/teams/backend", map[string]any{"members": []string{"alice@example.com", "bob@example.com"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("admin update team = %d, body = %s", rec.Code, rec.Body.String())
	}
	got = templatesByName(t, requestAs(t, s, bob, http.MethodGet, "/api/templates", nil))
	if _, ok := got["go"]; !ok {
		t.Fatalf("bob should see go after team update: %+v", got)
	}

	rec = requestAs(t, s, admin, http.MethodPut, "/api/teams/ops", map[string]any{"members": []string{}})
	if rec.Code != http.StatusOK {
		t.Fatalf("create ops team = %d, body = %s", rec.Code, rec.Body.String())
	}
	rec = requestAs(t, s, dev, http.MethodPut, "/api/templates/go/access", map[string]any{"users": []string{}, "teams": []string{"ops"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("assign ops team = %d, body = %s", rec.Code, rec.Body.String())
	}
	got = templatesByName(t, requestAs(t, s, bob, http.MethodGet, "/api/templates", nil))
	if _, ok := got["go"]; ok {
		t.Fatalf("bob should lose go after team reassignment: %+v", got)
	}
	if rec := requestAs(t, s, dev, http.MethodPut, "/api/templates/go/access", map[string]any{"teams": []string{"missing"}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown team = %d", rec.Code)
	}

	if rec := requestAs(t, s, admin, http.MethodDelete, "/api/teams/ops", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete ops = %d", rec.Code)
	}
	cfg, err := s.RBAC.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Teams["ops"]; ok {
		t.Fatalf("ops team still exists: %+v", cfg.Teams)
	}
	if access := cfg.Templates["go"]; len(access.Teams) != 0 || len(access.Owners) != 1 {
		t.Fatalf("go access after team delete = %+v", access)
	}

	if rec := requestAs(t, s, admin, http.MethodPut, "/api/teams/bad%20team", map[string]any{"members": []string{}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid team name = %d", rec.Code)
	}
}
