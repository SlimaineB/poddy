package server

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestDesktopSSHBridgesWebSocketToWorkspace(t *testing.T) {
	t.Parallel()
	serverConn, workspaceConn := net.Pipe()
	defer workspaceConn.Close()
	s := &Server{
		DesktopSecret: []byte("desktop-test-secret"),
		DesktopDial: func(_ context.Context, workspace string, port int) (net.Conn, error) {
			if workspace != "alice-ws" || port != 22 {
				t.Fatalf("dial = %s:%d", workspace, port)
			}
			return serverConn, nil
		},
	}
	httpServer := httptest.NewServer(http.HandlerFunc(s.desktopSSH))
	defer httpServer.Close()

	ticket, err := s.signDesktopTicket(desktopClaims{
		Owner:     "alice@example.com",
		Workspace: "alice-ws",
		Audience:  desktopTicketAudience,
		Expires:   time.Now().Add(time.Minute).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	target := "ws" + httpServer.URL[len("http"):] + "?workspace=alice-ws&ticket=" + url.QueryEscape(ticket)
	client, _, err := websocket.DefaultDialer.Dial(target, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	if err := client.WriteMessage(websocket.BinaryMessage, []byte("ping")); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 4)
	if _, err := io.ReadFull(workspaceConn, got); err != nil {
		t.Fatal(err)
	}
	if string(got) != "ping" {
		t.Fatalf("workspace read %q", got)
	}

	go func() {
		_, _ = workspaceConn.Write([]byte("pong"))
	}()
	_, payload, err := client.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != "pong" {
		t.Fatalf("client read %q", payload)
	}
}

func TestDesktopSSHRejectsMissingTicket(t *testing.T) {
	t.Parallel()
	s := &Server{DesktopSecret: []byte("secret"), DesktopDial: func(context.Context, string, int) (net.Conn, error) {
		t.Fatal("dial should not be called")
		return nil, nil
	}}
	rec := httptest.NewRecorder()
	s.desktopSSH(rec, httptest.NewRequest(http.MethodGet, "/api/desktop/ssh", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}
