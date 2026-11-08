package metadata

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"poddy/internal/k8s"
	"poddy/internal/rbac"
	"poddy/internal/templates"
	"poddy/internal/userstore"
)

func TestPostgresStore(t *testing.T) {
	url := os.Getenv("PODDY_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set PODDY_TEST_DATABASE_URL to run PostgreSQL integration tests")
	}

	ctx := context.Background()
	store, err := Open(ctx, url, true)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	pg, ok := store.(*Postgres)
	if !ok {
		t.Fatalf("store = %T", store)
	}

	namespace := fmt.Sprintf("poddy-test-%d", time.Now().UnixNano())
	if _, err := pg.pool.Exec(ctx, "DELETE FROM poddy.tenants WHERE namespace = $1", namespace); err != nil {
		t.Fatal(err)
	}
	defer pg.pool.Exec(ctx, "DELETE FROM poddy.tenants WHERE namespace = $1", namespace)

	cfg := rbac.Config{
		Admins: []string{"admin@example.com"},
		Teams: map[string]rbac.Team{
			"backend": {Members: []string{"alice@example.com", "group:devs"}},
		},
		Templates: map[string]rbac.Access{
			"base": {Owners: []string{"dev@example.com"}, Users: []string{"alice@example.com"}, Teams: []string{"backend"}},
		},
	}
	if err := store.SyncRBAC(ctx, namespace, cfg); err != nil {
		t.Fatal(err)
	}
	catalog, err := templates.Load("")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SyncTemplates(ctx, namespace, catalog, cfg); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	user := userstore.User{
		Subject:   "subject-1",
		Username:  "alice",
		Email:     "Alice@Example.com",
		Name:      "Alice",
		Groups:    []string{"devs"},
		FirstSeen: now,
		LastSeen:  now,
	}
	if err := store.SyncUser(ctx, namespace, user); err != nil {
		t.Fatal(err)
	}

	snapshot := k8s.WorkspaceSnapshot{
		Name:            "demo",
		Template:        "base",
		TemplateVersion: "1.2.3",
		Owner:           "alice@example.com",
		Image:           "example/workspace:1",
		Persistent:      true,
		Storage:         "5Gi",
		StorageClass:    "standard",
		Parameters:      map[string]string{"cpu": "2"},
		Labels:          map[string]string{"poddy.io/workspace": "demo"},
		Annotations:     map[string]string{"poddy.io/owner": "alice@example.com"},
		RenderedPod:     []byte(`{"containers":[{"name":"workspace","image":"example/workspace:1"}]}`),
		PortalURL:       "https://poddy.example.com",
		Phase:           "Running",
		DesiredState:    "running",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := store.SyncWorkspace(ctx, namespace, snapshot); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordAudit(ctx, Event{
		Namespace:  namespace,
		Actor:      "alice@example.com",
		ActorType:  "user",
		Action:     "workspace.create",
		Outcome:    "success",
		TargetType: "workspace",
		TargetName: "demo",
		Workspace:  "demo",
		SourceIP:   "203.0.113.7",
		UserAgent:  "test",
		RequestID:  "req-1",
		Duration:   42 * time.Millisecond,
		Details:    map[string]any{"template": "base"},
	}); err != nil {
		t.Fatal(err)
	}

	var admins, teams, members, templateVersions int
	if err := pg.pool.QueryRow(ctx, "SELECT count(*) FROM poddy.global_admins WHERE namespace = $1", namespace).Scan(&admins); err != nil {
		t.Fatal(err)
	}
	if err := pg.pool.QueryRow(ctx, "SELECT count(*) FROM poddy.teams WHERE namespace = $1", namespace).Scan(&teams); err != nil {
		t.Fatal(err)
	}
	if err := pg.pool.QueryRow(ctx, "SELECT count(*) FROM poddy.team_members WHERE namespace = $1", namespace).Scan(&members); err != nil {
		t.Fatal(err)
	}
	if err := pg.pool.QueryRow(ctx, "SELECT count(*) FROM poddy.template_versions WHERE namespace = $1 AND is_active", namespace).Scan(&templateVersions); err != nil {
		t.Fatal(err)
	}
	if admins != 1 || teams != 1 || members != 2 || templateVersions < 2 {
		t.Fatalf("counts admins=%d teams=%d members=%d templateVersions=%d", admins, teams, members, templateVersions)
	}

	if _, err := pg.pool.Exec(ctx, "INSERT INTO poddy.templates (namespace, name) VALUES ($1, 'stale')", namespace); err != nil {
		t.Fatal(err)
	}
	if err := store.SyncTemplates(ctx, namespace, catalog, cfg); err != nil {
		t.Fatal(err)
	}
	var stale int
	if err := pg.pool.QueryRow(ctx, "SELECT count(*) FROM poddy.templates WHERE namespace = $1 AND name = 'stale'", namespace).Scan(&stale); err != nil {
		t.Fatal(err)
	}
	if stale != 0 {
		t.Fatalf("stale templates = %d", stale)
	}

	var identity, email string
	if err := pg.pool.QueryRow(ctx, "SELECT identity, email FROM poddy.users WHERE namespace = $1", namespace).Scan(&identity, &email); err != nil {
		t.Fatal(err)
	}
	if identity != "alice@example.com" || email != "alice@example.com" {
		t.Fatalf("user identity=%q email=%q", identity, email)
	}

	var desired, templateVersion string
	if err := pg.pool.QueryRow(ctx, "SELECT desired_state, template_version FROM poddy.workspaces WHERE namespace = $1 AND name = 'demo'", namespace).Scan(&desired, &templateVersion); err != nil {
		t.Fatal(err)
	}
	if desired != "running" || templateVersion != "1.2.3" {
		t.Fatalf("workspace desired=%q version=%q", desired, templateVersion)
	}

	if _, err := pg.pool.Exec(ctx, `
		INSERT INTO poddy.workspaces (namespace, name, owner, image, rendered_pod)
		VALUES ($1, 'stale', 'nobody@example.com', 'example/stale:1', '{}')`, namespace); err != nil {
		t.Fatal(err)
	}
	if err := store.PruneWorkspaces(ctx, namespace, []string{"demo"}); err != nil {
		t.Fatal(err)
	}
	var staleWorkspaces int
	if err := pg.pool.QueryRow(ctx, "SELECT count(*) FROM poddy.workspaces WHERE namespace = $1 AND name = 'stale'", namespace).Scan(&staleWorkspaces); err != nil {
		t.Fatal(err)
	}
	if staleWorkspaces != 0 {
		t.Fatalf("stale workspaces = %d", staleWorkspaces)
	}

	var action, outcome, actor, sourceIP string
	var duration int
	var detail string
	if err := pg.pool.QueryRow(ctx, `
		SELECT action, outcome, actor, host(source_ip), duration_ms, details->>'template'
		FROM poddy.audit_events WHERE namespace = $1`, namespace,
	).Scan(&action, &outcome, &actor, &sourceIP, &duration, &detail); err != nil {
		t.Fatal(err)
	}
	if action != "workspace.create" || outcome != "success" || actor != "alice@example.com" || sourceIP != "203.0.113.7" || duration != 42 || detail != "base" {
		t.Fatalf("audit action=%q outcome=%q actor=%q ip=%q duration=%d detail=%q", action, outcome, actor, sourceIP, duration, detail)
	}

	page, err := store.ListAudit(ctx, namespace, AuditFilter{
		Query:     "base",
		Action:    "workspace.create",
		Outcome:   "success",
		Actor:     "alice@example.com",
		Workspace: "demo",
		Page:      1,
		PerPage:   10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.Items) != 1 {
		t.Fatalf("audit page = %+v", page)
	}
	item := page.Items[0]
	if item.Action != "workspace.create" || item.Actor != "alice@example.com" || item.TargetName != "demo" || item.SourceIP != "203.0.113.7" {
		t.Fatalf("audit item = %+v", item)
	}
	if item.DurationMS == nil || *item.DurationMS != 42 || item.Details["template"] != "base" {
		t.Fatalf("audit item details = %+v", item)
	}

	snapshot.Phase = "Stopped"
	snapshot.DesiredState = "stopped"
	snapshot.StoppedAt = now.Add(time.Minute)
	if err := store.SyncWorkspace(ctx, namespace, snapshot); err != nil {
		t.Fatal(err)
	}
	if err := pg.pool.QueryRow(ctx, "SELECT desired_state FROM poddy.workspaces WHERE namespace = $1 AND name = 'demo'", namespace).Scan(&desired); err != nil {
		t.Fatal(err)
	}
	if desired != "stopped" {
		t.Fatalf("desired_state = %q", desired)
	}

	if err := store.DeleteWorkspace(ctx, namespace, "demo"); err != nil {
		t.Fatal(err)
	}
	var workspaces int
	if err := pg.pool.QueryRow(ctx, "SELECT count(*) FROM poddy.workspaces WHERE namespace = $1", namespace).Scan(&workspaces); err != nil {
		t.Fatal(err)
	}
	if workspaces != 0 {
		t.Fatalf("workspaces = %d", workspaces)
	}

	if err := ApplyMigrations(ctx, pg.pool); err != nil {
		t.Fatal(err)
	}
}
