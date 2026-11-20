package bastion

import (
	"context"
	"encoding/base64"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"

	"poddy/internal/k8s"
	"poddy/internal/tunnel"
)

func (b *Bastion) AgentHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/internal/dial" {
			b.handleInternalDial(w, r)
			return
		}
		if b.Draining() {
			w.Header().Set("Retry-After", "1")
			http.Error(w, "draining", http.StatusServiceUnavailable)
			return
		}
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
			log.Printf("ssh agent %s: %v", strings.ReplaceAll(strings.ReplaceAll(claims.Workspace, "\n", " "), "\r", " "), strings.ReplaceAll(strings.ReplaceAll(err.Error(), "\n", " "), "\r", " "))
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

func (b *Bastion) handleInternalDial(w http.ResponseWriter, r *http.Request) {
	kind := strings.TrimSpace(r.Header.Get("X-Poddy-Dial-Kind"))
	encodedName := strings.TrimSpace(r.Header.Get("X-Poddy-Dial-Name"))
	target := strings.TrimSpace(r.Header.Get("X-Poddy-Dial-Target"))
	timestamp := strings.TrimSpace(r.Header.Get("X-Poddy-Dial-Time"))
	signature := strings.TrimSpace(r.Header.Get("X-Poddy-Dial-Auth"))
	nameBytes, err := base64.RawURLEncoding.DecodeString(encodedName)
	if err != nil {
		http.Error(w, "invalid name", http.StatusBadRequest)
		return
	}
	name := string(nameBytes)
	if !tunnel.VerifyDial(b.InternalKey, timestamp, kind, name, target, signature) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	var upstream net.Conn
	switch kind {
	case tunnel.KindWorkspace:
		if b.Broker == nil {
			http.Error(w, "workspace tunnel is not configured", http.StatusNotFound)
			return
		}
		upstream, err = b.Broker.Dial(ctx, name, target)
	case tunnel.KindRegion:
		if b.RegionBroker == nil {
			http.Error(w, "region tunnel is not configured", http.StatusNotFound)
			return
		}
		upstream, err = b.RegionBroker.Dial(ctx, name, target)
	default:
		http.Error(w, "invalid kind", http.StatusBadRequest)
		return
	}
	if err != nil {
		http.Error(w, "cannot reach target", http.StatusBadGateway)
		return
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		_ = upstream.Close()
		http.Error(w, "streaming is not supported", http.StatusInternalServerError)
		return
	}
	conn, buffered, err := hijacker.Hijack()
	if err != nil {
		_ = upstream.Close()
		return
	}
	defer conn.Close()
	defer upstream.Close()
	_, _ = buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
	if err := buffered.Flush(); err != nil {
		return
	}
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(upstream, conn)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(conn, upstream)
		done <- struct{}{}
	}()
	<-done
}
