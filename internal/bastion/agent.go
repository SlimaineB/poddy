package bastion

import (
	"log"
	"net"
	"net/http"
	"strings"

	corev1 "k8s.io/api/core/v1"

	"poddy/internal/k8s"
)

func (b *Bastion) AgentHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if b.Broker == nil {
			http.Error(w, "agent tunnel is not configured", http.StatusNotFound)
			return
		}
		claims, err := b.Broker.Authenticate(r)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if header := strings.TrimSpace(r.Header.Get("X-Poddy-Agent-Workspace")); header != "" && header != claims.Workspace {
			http.Error(w, "workspace mismatch", http.StatusForbidden)
			return
		}
		pod, err := b.Manager.Get(r.Context(), claims.Workspace)
		if err != nil {
			http.Error(w, "workspace not found", http.StatusNotFound)
			return
		}
		if !strings.EqualFold(k8s.Owner(pod), claims.Owner) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if pod.Status.Phase != corev1.PodRunning {
			http.Error(w, "workspace is not running", http.StatusConflict)
			return
		}

		sourceIP := remoteIP(r.RemoteAddr)
		b.recordEvent(claims.Workspace, claims.Owner, "workspace", sourceIP, "ssh.agent.connect", "success", "")
		if err := b.Broker.ServeAgent(w, r, claims); err != nil {
			log.Printf("ssh agent %s: %v", claims.Workspace, err)
			b.recordEvent(claims.Workspace, claims.Owner, "workspace", sourceIP, "ssh.agent.disconnect", "error", err.Error())
			return
		}
		b.recordEvent(claims.Workspace, claims.Owner, "workspace", sourceIP, "ssh.agent.disconnect", "success", "")
	})
}

func remoteIP(address string) string {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return address
	}
	return host
}
