package server

import (
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

var desktopSSHUpgrader = websocket.Upgrader{
	HandshakeTimeout: 15 * time.Second,
	ReadBufferSize:   32 << 10,
	WriteBufferSize:  32 << 10,
	CheckOrigin:      func(*http.Request) bool { return true },
}

func (s *Server) desktopSSH(w http.ResponseWriter, r *http.Request) {
	if s.DesktopDial == nil || len(s.DesktopSecret) == 0 {
		http.Error(w, "desktop SSH is not configured", http.StatusNotImplemented)
		return
	}
	claims, err := s.verifyDesktopTicket(strings.TrimSpace(r.URL.Query().Get("ticket")))
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	if requested := strings.TrimSpace(r.URL.Query().Get("workspace")); requested != "" && requested != claims.Workspace {
		http.Error(w, "workspace does not match ticket", http.StatusForbidden)
		return
	}

	workspaceConn, err := s.DesktopDial(r.Context(), claims.Workspace, 22)
	if err != nil {
		log.Printf("desktop ssh %s: %v", claims.Workspace, err)
		http.Error(w, "workspace is not reachable", http.StatusBadGateway)
		return
	}
	defer workspaceConn.Close()

	ws, err := desktopSSHUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer ws.Close()
	ws.SetReadLimit(1 << 20)

	done := make(chan struct{}, 2)
	go func() {
		_ = copyWebSocketToConn(workspaceConn, ws)
		done <- struct{}{}
	}()
	go func() {
		_ = copyConnToWebSocket(ws, workspaceConn)
		done <- struct{}{}
	}()
	<-done
}

func copyWebSocketToConn(dst net.Conn, ws *websocket.Conn) error {
	for {
		_, reader, err := ws.NextReader()
		if err != nil {
			if !isExpectedWebSocketClose(err) {
				return err
			}
			return nil
		}
		if _, err := io.Copy(dst, reader); err != nil {
			return err
		}
	}
}

func copyConnToWebSocket(ws *websocket.Conn, src net.Conn) error {
	buf := make([]byte, 32<<10)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			if writeErr := ws.WriteMessage(websocket.BinaryMessage, buf[:n]); writeErr != nil {
				return writeErr
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				_ = ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
				return nil
			}
			return err
		}
	}
}

func isExpectedWebSocketClose(err error) bool {
	return websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway, websocket.CloseNoStatusReceived)
}
