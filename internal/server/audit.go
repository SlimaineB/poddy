package server

import (
	"bufio"
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"poddy/internal/metadata"
	"poddy/internal/rbac"
	"poddy/internal/userstore"
)

const metadataTimeout = 5 * time.Second

type auditContextKey struct{}

type auditInfo struct {
	targetName string
	details    map[string]any
}

func setAuditTarget(r *http.Request, name string) {
	if info, ok := r.Context().Value(auditContextKey{}).(*auditInfo); ok {
		info.targetName = name
	}
}

func setAuditDetail(r *http.Request, key string, value any) {
	if info, ok := r.Context().Value(auditContextKey{}).(*auditInfo); ok {
		if info.details == nil {
			info.details = map[string]any{}
		}
		info.details[key] = value
	}
}

func (s *Server) audit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		original := r
		info := &auditInfo{}
		r = r.WithContext(context.WithValue(r.Context(), auditContextKey{}, info))
		sw := &statusWriter{ResponseWriter: w}
		start := time.Now()

		next.ServeHTTP(sw, r)
		original.Pattern = r.Pattern

		action, targetType := auditRoute(r)
		if action == "" {
			return
		}
		target := info.targetName
		if target == "" {
			target = r.PathValue("name")
		}
		if target == "" {
			target = r.PathValue("id")
		}
		outcome := auditOutcome(sw.status)
		s.auditEvent(r, metadata.Event{
			Action:     action,
			Outcome:    outcome,
			TargetType: targetType,
			TargetName: target,
			Workspace:  workspaceTarget(targetType, target),
			Duration:   time.Since(start),
			Details:    info.details,
		})
	})
}

func auditRoute(r *http.Request) (string, string) {
	switch r.Pattern {
	case "POST /api/workspaces":
		return "workspace.create", "workspace"
	case "DELETE /api/workspaces/{name}":
		return "workspace.delete", "workspace"
	case "POST /api/workspaces/{name}/stop":
		return "workspace.stop", "workspace"
	case "POST /api/workspaces/{name}/start":
		return "workspace.start", "workspace"
	case "POST /api/workspaces/{name}/restart":
		return "workspace.restart", "workspace"
	case "PUT /api/workspaces/{name}/schedule":
		return "workspace.schedule.update", "workspace"
	case "GET /api/audit":
		return "audit.view", "audit"
	case "POST /api/me/ssh-certificate":
		return "ssh.certificate.issue", "user"
	case "GET /api/workspaces/{name}/logs":
		return "workspace.logs.view", "workspace"
	case "GET /api/workspaces/{name}/manifests":
		return "workspace.manifests.view", "workspace"
	case "POST /api/workspaces/{name}/vscode":
		return "workspace.vscode.open", "workspace"
	case "POST /api/workspaces/{name}/cursor":
		return "workspace.cursor.open", "workspace"
	case "POST /api/workspaces/{name}/gateway":
		return "workspace.gateway.open", "workspace"
	case "POST /api/workspaces/{name}/desktop":
		return "workspace.desktop.open", "workspace"
	case "PUT /api/templates/{name}/access":
		return "template.access.update", "template"
	case "PUT /api/teams/{name}":
		return "team.update", "team"
	case "DELETE /api/teams/{name}":
		return "team.delete", "team"
	case "PUT /api/user-quotas/{identity}":
		return "user_quota.update", "user"
	case "DELETE /api/user-quotas/{identity}":
		return "user_quota.delete", "user"
	case "POST /api/external-auth/{id}/disconnect":
		return "external_auth.disconnect", "provider"
	}
	return "", ""
}

func auditOutcome(status int) string {
	switch {
	case status == 0 || status < 400:
		return "success"
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return "denied"
	default:
		return "error"
	}
}

func workspaceTarget(targetType, target string) string {
	if targetType == "workspace" {
		return target
	}
	return ""
}

func (s *Server) auditEvent(r *http.Request, event metadata.Event) {
	if s.Metadata == nil {
		return
	}
	if event.Namespace == "" && s.Workspaces != nil {
		event.Namespace = s.Workspaces.Namespace
	}
	if event.Actor == "" {
		event.Actor, event.ActorType = s.auditActor(r)
	}
	if event.ActorType == "" {
		event.ActorType = "user"
	}
	if event.OccurredAt.IsZero() {
		event.OccurredAt = time.Now().UTC()
	}
	if event.SourceIP == "" {
		event.SourceIP = clientIP(r)
	}
	if event.UserAgent == "" {
		event.UserAgent = r.UserAgent()
	}
	if event.RequestID == "" {
		event.RequestID = requestID(r)
	}
	ctx, cancel := context.WithTimeout(context.Background(), metadataTimeout)
	defer cancel()
	if err := s.Metadata.RecordAudit(ctx, event); err != nil {
		log.Printf("audit: %s: %v", event.Action, err)
	}
}

func (s *Server) auditActor(r *http.Request) (string, string) {
	id, ok := s.identify(r)
	if !ok {
		if s.Token == "" && s.OIDC == nil {
			return "local", "system"
		}
		return "", ""
	}
	switch id.Method {
	case "token":
		return "token", "token"
	case "oidc":
		return ownerID(id.User), "user"
	default:
		return ownerID(id.User), "user"
	}
}

func clientIP(r *http.Request) string {
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		return strings.TrimSpace(strings.Split(forwarded, ",")[0])
	}
	if real := r.Header.Get("X-Real-IP"); real != "" {
		return strings.TrimSpace(real)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

func requestID(r *http.Request) string {
	return strings.TrimSpace(r.Header.Get("X-Request-Id"))
}

func (s *Server) syncRBAC(ctx context.Context, cfg rbac.Config) {
	if s.Metadata == nil || s.Workspaces == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, metadataTimeout)
	defer cancel()
	if err := s.Metadata.SyncRBAC(ctx, s.Workspaces.Namespace, cfg); err != nil {
		log.Printf("metadata: sync rbac: %v", err)
	}
}

func (s *Server) syncTemplates(ctx context.Context, cfg rbac.Config) {
	if s.Metadata == nil || s.Workspaces == nil || s.Workspaces.Templates == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, metadataTimeout)
	defer cancel()
	if err := s.Metadata.SyncTemplates(ctx, s.Workspaces.Namespace, s.Workspaces.Templates, cfg); err != nil {
		log.Printf("metadata: sync templates: %v", err)
	}
}

func (s *Server) syncWorkspace(ctx context.Context, name string) {
	if s.Metadata == nil || s.Workspaces == nil {
		return
	}
	snapshot, err := s.Workspaces.Snapshot(ctx, name)
	if err != nil {
		log.Printf("metadata: snapshot workspace %s: %v", name, err)
		return
	}
	ctx, cancel := context.WithTimeout(ctx, metadataTimeout)
	defer cancel()
	if err := s.Metadata.SyncWorkspace(ctx, s.Workspaces.Namespace, snapshot); err != nil {
		log.Printf("metadata: sync workspace %s: %v", name, err)
	}
}

func (s *Server) deleteWorkspaceMetadata(ctx context.Context, name string) {
	if s.Metadata == nil || s.Workspaces == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, metadataTimeout)
	defer cancel()
	if err := s.Metadata.DeleteWorkspace(ctx, s.Workspaces.Namespace, name); err != nil {
		log.Printf("metadata: delete workspace %s: %v", name, err)
	}
}

func (s *Server) syncUser(ctx context.Context, user userstore.User) {
	if s.Metadata == nil || s.Workspaces == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, metadataTimeout)
	defer cancel()
	if err := s.Metadata.SyncUser(ctx, s.Workspaces.Namespace, user); err != nil {
		log.Printf("metadata: sync user %s: %v", metadata.UserIdentity(user), err)
	}
}

func (s *Server) auditDenied(r *http.Request, action, targetType, targetName string) {
	s.auditEvent(r, metadata.Event{
		Action:     action,
		Outcome:    "denied",
		TargetType: targetType,
		TargetName: targetName,
	})
}

// statusWriter records the HTTP status without breaking hijacking (terminal
// websockets) or streaming responses.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(data)
}

func (w *statusWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("response writer does not support hijacking")
	}
	if w.status == 0 {
		w.status = http.StatusSwitchingProtocols
	}
	return hijacker.Hijack()
}

func (w *statusWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

var _ http.Hijacker = (*statusWriter)(nil)
var _ http.Flusher = (*statusWriter)(nil)
