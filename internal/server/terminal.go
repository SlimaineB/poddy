package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/remotecommand"

	"poddy/internal/metadata"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(*http.Request) bool { return true },
}

type controlMessage struct {
	Type string `json:"type"`
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

func (s *Server) terminal(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
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
	allowed, err := s.workspaceAllowed(r, name, id, cfg)
	if err != nil || !allowed {
		s.auditTerminal(r, name, "denied", "workspace not found")
		http.Error(w, "workspace not found", http.StatusNotFound)
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

	exec, err := remotecommand.NewSPDYExecutor(
		s.Workspaces.Rest,
		http.MethodPost,
		s.Workspaces.ExecRequest(name, []string{"/bin/bash"}).URL(),
	)
	if err != nil {
		writeWS(conn, err.Error())
		return
	}

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

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
		out.Write([]byte("\r\n" + err.Error() + "\r\n"))
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
	conn.WriteMessage(websocket.BinaryMessage, []byte(message))
}

type wsWriter struct {
	mu   sync.Mutex
	conn *websocket.Conn
}

func (w *wsWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
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
