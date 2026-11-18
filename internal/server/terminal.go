package server

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/remotecommand"

	"poddy/internal/metadata"
)

const (
	wsMaxMessage = 1 << 20
	wsPongWait   = 60 * time.Second
	wsPingPeriod = 30 * time.Second
	wsWriteWait  = 10 * time.Second
)

var upgrader = websocket.Upgrader{
	HandshakeTimeout: 10 * time.Second,
	CheckOrigin:      sameOriginRequest,
}

func sameOriginRequest(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return false
	}
	hosts := []string{r.Host}
	for _, value := range r.Header.Values("X-Forwarded-Host") {
		for _, host := range strings.Split(value, ",") {
			hosts = append(hosts, host)
		}
	}
	want := canonicalHost(parsed.Host)
	for _, host := range hosts {
		if canonicalHost(host) == want {
			return true
		}
	}
	return false
}

func canonicalHost(host string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return ""
	}
	if name, port, err := net.SplitHostPort(host); err == nil {
		if port == "80" || port == "443" {
			return strings.ToLower(name)
		}
		return strings.ToLower(net.JoinHostPort(name, port))
	}
	return strings.ToLower(host)
}

type controlMessage struct {
	Type string `json:"type"`
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

func (s *Server) terminal(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if s.Regions != nil {
		s.proxyWorkspace(w, r)
		return
	}
	if name == "" {
		http.Error(w, "missing workspace name", http.StatusBadRequest)
		return
	}
	id, ok := s.currentIdentity(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	cfg, err := s.loadRBAC(r)
	if err != nil {
		http.Error(w, "rbac: "+err.Error(), http.StatusInternalServerError)
		return
	}
	decision, err := s.interactiveAccess(r, name, id, cfg)
	if err != nil {
		s.auditTerminal(r, name, "denied", "workspace not found")
		http.Error(w, "workspace not found", http.StatusNotFound)
		return
	}
	if !decision.Allowed {
		s.auditTerminal(r, name, "denied", "owner approval required")
		s.denyInteractive(w, id, cfg)
		return
	}

	pod, err := s.Workspaces.Get(r.Context(), name)
	if err != nil {
		s.auditTerminal(r, name, "error", err.Error())
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if pod.Status.Phase != corev1.PodRunning {
		s.auditTerminal(r, name, "error", "workspace is not running")
		http.Error(w, "workspace is not running", http.StatusConflict)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.auditTerminal(r, name, "error", err.Error())
		return
	}
	defer conn.Close()
	s.auditTerminal(r, name, "success", "")
	stopPresence := s.trackPresence(name, ownerID(id.User), "terminal")
	defer stopPresence()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	conn.SetReadLimit(wsMaxMessage)
	_ = conn.SetReadDeadline(time.Now().Add(wsPongWait))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(wsPongWait))
	})
	go pingLoop(ctx, conn)

	exec, err := remotecommand.NewSPDYExecutor(
		s.Workspaces.Rest,
		http.MethodPost,
		s.Workspaces.ExecRequest(name, []string{"/bin/sh", "-c", `if [ "$(id -u)" = "0" ] && command -v su >/dev/null 2>&1 && id dev >/dev/null 2>&1; then exec su - dev; else exec /bin/bash; fi`}).URL(),
	)
	if err != nil {
		writeWS(conn, err.Error())
		return
	}

	stdinR, stdinW := io.Pipe()
	sizes := newSizeQueue()
	defer sizes.Close()

	out := &wsWriter{conn: conn}

	go func() {
		defer cancel()
		defer stdinW.Close()
		for {
			messageType, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if messageType == websocket.TextMessage {
				var msg controlMessage
				if json.Unmarshal(data, &msg) == nil && msg.Type == "resize" {
					sizes.Push(msg.Cols, msg.Rows)
					continue
				}
			}
			if _, err := stdinW.Write(data); err != nil {
				return
			}
		}
	}()

	err = exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdin:             stdinR,
		Stdout:            out,
		Stderr:            out,
		Tty:               true,
		TerminalSizeQueue: sizes,
	})
	if err != nil {
		_, _ = out.Write([]byte("\r\n" + err.Error() + "\r\n"))
	}
}

func (s *Server) auditTerminal(r *http.Request, name, outcome, message string) {
	s.auditEvent(r, metadata.Event{
		Action:     "workspace.terminal.open",
		Outcome:    outcome,
		TargetType: "workspace",
		TargetName: name,
		Workspace:  name,
		Error:      message,
	})
}

func writeWS(conn *websocket.Conn, message string) {
	_ = conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
	_ = conn.WriteMessage(websocket.BinaryMessage, []byte(message))
}

func pingLoop(ctx context.Context, conn *websocket.Conn) {
	ticker := time.NewTicker(wsPingPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(wsWriteWait)); err != nil {
				return
			}
		}
	}
}

type wsWriter struct {
	mu   sync.Mutex
	conn *websocket.Conn
}

func (w *wsWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	_ = w.conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
	if err := w.conn.WriteMessage(websocket.BinaryMessage, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

type sizeQueue struct {
	ch   chan remotecommand.TerminalSize
	done chan struct{}
	once sync.Once
}

func newSizeQueue() *sizeQueue {
	return &sizeQueue{
		ch:   make(chan remotecommand.TerminalSize, 8),
		done: make(chan struct{}),
	}
}

func (q *sizeQueue) Push(cols, rows uint16) {
	if cols == 0 || rows == 0 {
		return
	}
	select {
	case q.ch <- remotecommand.TerminalSize{Width: cols, Height: rows}:
	default:
	}
}

func (q *sizeQueue) Next() *remotecommand.TerminalSize {
	select {
	case size := <-q.ch:
		return &size
	case <-q.done:
		return nil
	}
}

func (q *sizeQueue) Close() {
	q.once.Do(func() { close(q.done) })
}
