// Package metadata persists optional control-plane metadata and audit events in
// PostgreSQL. Kubernetes remains the runtime source of truth; the PostgreSQL
// backend mirrors state so it can be audited, backed up, and restored.
package metadata

import (
	"context"
	"strings"
	"time"

	"poddy/internal/k8s"
	"poddy/internal/rbac"
	"poddy/internal/templates"
	"poddy/internal/userstore"
)

// Event is one append-only control-plane audit record.
type Event struct {
	Namespace       string
	OccurredAt      time.Time
	Actor           string
	ActorType       string
	Action          string
	Outcome         string
	TargetType      string
	TargetName      string
	TargetNamespace string
	Workspace       string
	SourceIP        string
	UserAgent       string
	RequestID       string
	Duration        time.Duration
	Error           string
	Details         map[string]any
}

// Auditor records audit events. The SSH bastion only needs this subset.
type Auditor interface {
	RecordAudit(ctx context.Context, event Event) error
}

// Store mirrors Kubernetes metadata and records audit events.
type Store interface {
	Auditor
	SyncRBAC(ctx context.Context, namespace string, cfg rbac.Config) error
	SyncTemplates(ctx context.Context, namespace string, catalog *templates.Catalog, cfg rbac.Config) error
	SyncUser(ctx context.Context, namespace string, user userstore.User) error
	SyncWorkspace(ctx context.Context, namespace string, snapshot k8s.WorkspaceSnapshot) error
	PruneWorkspaces(ctx context.Context, namespace string, names []string) error
	DeleteWorkspace(ctx context.Context, namespace, name string) error
	Close() error
}

// Nop is the disabled metadata backend.
type Nop struct{}

func (Nop) RecordAudit(context.Context, Event) error { return nil }

func (Nop) SyncRBAC(context.Context, string, rbac.Config) error { return nil }

func (Nop) SyncTemplates(context.Context, string, *templates.Catalog, rbac.Config) error {
	return nil
}

func (Nop) SyncUser(context.Context, string, userstore.User) error { return nil }

func (Nop) SyncWorkspace(context.Context, string, k8s.WorkspaceSnapshot) error { return nil }

func (Nop) PruneWorkspaces(context.Context, string, []string) error { return nil }

func (Nop) DeleteWorkspace(context.Context, string, string) error { return nil }

func (Nop) Close() error { return nil }

// UserIdentity returns the stable RBAC identity for a user. It matches the
// owner identity used for workspaces.
func UserIdentity(user userstore.User) string {
	switch {
	case strings.TrimSpace(user.Email) != "":
		return strings.ToLower(strings.TrimSpace(user.Email))
	case strings.TrimSpace(user.Username) != "":
		return strings.ToLower(strings.TrimSpace(user.Username))
	default:
		return strings.TrimSpace(user.Subject)
	}
}
