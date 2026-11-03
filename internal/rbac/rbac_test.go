package rbac

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestParseAndRules(t *testing.T) {
	cfg, err := Parse(`
admins:
  - admin@example.com
  - group:platform
teams:
  backend:
    members:
      - alice@example.com
templates:
  rust:
    teams:
      - backend
  go:
    owners:
      - dev@example.com
    users:
      - alice@example.com
      - group:backend
  node:
    owners:
      - user:dev
    users:
      - "*"
`)
	if err != nil {
		t.Fatal(err)
	}

	admin := Identity{Username: "root", Email: "admin@example.com"}
	if !cfg.IsAdmin(admin) {
		t.Fatal("email admin not matched")
	}
	if !cfg.IsAdmin(Identity{Username: "ops", Groups: []string{"Platform"}}) {
		t.Fatal("group admin not matched")
	}

	owner := Identity{Username: "dev", Email: "dev@example.com"}
	if !cfg.IsOwner("go", owner) || !cfg.CanManage("go", owner) {
		t.Fatal("owner not matched")
	}
	if !cfg.CanUse("go", owner) {
		t.Fatal("owner cannot use own template")
	}
	if !cfg.CanUse("go", Identity{Email: "alice@example.com"}) {
		t.Fatal("assigned user cannot use template")
	}
	if !cfg.CanUse("go", Identity{Username: "bob", Groups: []string{"backend"}}) {
		t.Fatal("assigned group cannot use template")
	}
	if cfg.CanUse("go", Identity{Username: "bob", Email: "bob@example.com"}) {
		t.Fatal("unassigned user can use restricted template")
	}
	if !cfg.CanUse("base", Identity{Username: "bob"}) {
		t.Fatal("template without access entry should be public")
	}
	if !cfg.CanUse("node", Identity{Username: "bob"}) {
		t.Fatal("wildcard user should allow everyone")
	}
	if !cfg.InTeam("backend", Identity{Email: "alice@example.com"}) {
		t.Fatal("team member not matched")
	}
	if !cfg.CanUse("rust", Identity{Email: "alice@example.com"}) {
		t.Fatal("team assignment should grant template access")
	}
	if cfg.CanUse("rust", Identity{Email: "bob@example.com"}) {
		t.Fatal("non-member can use team template")
	}
	if got := cfg.TeamNames(); len(got) != 1 || got[0] != "backend" {
		t.Fatalf("team names = %v", got)
	}
	if got := cfg.OwnedTemplates(owner); len(got) != 2 || got[0] != "go" || got[1] != "node" {
		t.Fatalf("owned templates = %v", got)
	}
}

func TestParseEmptyAndInvalid(t *testing.T) {
	cfg, err := Parse("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Admins != nil || cfg.Templates != nil {
		t.Fatalf("empty config = %+v", cfg)
	}
	if _, err := Parse("admins: ["); err == nil {
		t.Fatal("expected invalid yaml error")
	}
}

func TestNormalize(t *testing.T) {
	got := Normalize([]string{" alice@example.com ", "ALICE@example.com", "", "group:devs", "group:devs"})
	if len(got) != 2 || got[0] != "alice@example.com" || got[1] != "group:devs" {
		t.Fatalf("normalize = %v", got)
	}
}

func TestStoreRoundTrip(t *testing.T) {
	client := fake.NewClientset()
	store := NewStore(client, "poddy", "")
	ctx := context.Background()

	cfg, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Admins) != 0 {
		t.Fatalf("missing configmap should be empty: %+v", cfg)
	}

	want := Config{
		Admins: []string{"admin@example.com"},
		Teams: map[string]Team{
			"backend": {Members: []string{"alice@example.com"}},
		},
		Templates: map[string]Access{
			"go": {Owners: []string{"dev@example.com"}, Users: []string{"alice@example.com"}, Teams: []string{"backend"}},
		},
	}
	if err := store.Save(ctx, want); err != nil {
		t.Fatal(err)
	}

	got, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Admins) != 1 || got.Admins[0] != "admin@example.com" {
		t.Fatalf("admins = %v", got.Admins)
	}
	if access := got.Templates["go"]; len(access.Owners) != 1 || access.Owners[0] != "dev@example.com" || len(access.Teams) != 1 || access.Teams[0] != "backend" {
		t.Fatalf("go access = %+v", access)
	}
	if team := got.Teams["backend"]; len(team.Members) != 1 || team.Members[0] != "alice@example.com" {
		t.Fatalf("backend team = %+v", team)
	}

	want.Admins = append(want.Admins, "second@example.com")
	if err := store.Save(ctx, want); err != nil {
		t.Fatal(err)
	}
	got, err = store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Admins) != 2 {
		t.Fatalf("admins after update = %v", got.Admins)
	}

	cm, err := client.CoreV1().ConfigMaps("poddy").Get(ctx, DefaultConfigMap, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if cm.Data[DataKey] == "" {
		t.Fatal("rbac.yaml key is empty")
	}
}

func TestValidateTeamName(t *testing.T) {
	for _, name := range []string{"backend", "team-1", "a.b_c"} {
		if err := ValidateTeamName(name); err != nil {
			t.Fatalf("%q rejected: %v", name, err)
		}
	}
	for _, name := range []string{"", "bad team", "team/one", "-leading"} {
		if err := ValidateTeamName(name); err == nil {
			t.Fatalf("%q accepted", name)
		}
	}
}
