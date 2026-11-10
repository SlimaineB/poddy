package region

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"

	"poddy/internal/auth"
	"poddy/internal/k8s"
)

type directTunnel struct {
	target string
}

func (d directTunnel) Dial(ctx context.Context, region, target string) (net.Conn, error) {
	var dialer net.Dialer
	return dialer.DialContext(ctx, "tcp", d.target)
}

func (directTunnel) Connected(string) bool { return true }

func TestClientUsesTunnelWithoutURL(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/workspaces":
			_ = json.NewEncoder(w).Encode([]k8s.Workspace{{Name: "app", Phase: "Running"}})
		default:
			_, _ = io.WriteString(w, "proxied")
		}
	}))
	defer backend.Close()

	client := NewClient(Config{Name: "eu", Key: "secret"})
	client.SetTunnel(directTunnel{target: backend.Listener.Addr().String()})

	list, err := client.List(context.Background(), Identity{User: auth.User{Username: "alice"}, Admin: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Name != "app" || list[0].Region != "eu" {
		t.Fatalf("list = %+v", list)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/app/code/", nil)
	client.Proxy(rec, req, Identity{User: auth.User{Username: "alice"}, Admin: true})
	if rec.Code != http.StatusOK || rec.Body.String() != "proxied" {
		t.Fatalf("proxy status = %d body = %q", rec.Code, rec.Body.String())
	}
}

func TestParseAllowsTunnelOnlyRegion(t *testing.T) {
	registry, err := Parse([]byte("regions:\n- name: eu\n  key: secret\n"))
	if err != nil {
		t.Fatal(err)
	}
	cfg, ok := registry.Get("eu")
	if !ok || cfg.URL != "" {
		t.Fatalf("region = %+v, ok = %v", cfg, ok)
	}
}

func TestClientProxyWebSocketThroughTunnel(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			kind, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if err := conn.WriteMessage(kind, data); err != nil {
				return
			}
		}
	}))
	defer backend.Close()

	client := NewClient(Config{Name: "eu", Key: "secret"})
	client.SetTunnel(directTunnel{target: backend.Listener.Addr().String()})
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		client.Proxy(w, r, Identity{User: auth.User{Username: "alice"}, Admin: true})
	}))
	defer hub.Close()

	wsURL := "ws" + strings.TrimPrefix(hub.URL, "http") + "/api/workspaces/app/code/"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.WriteMessage(websocket.TextMessage, []byte("ping")); err != nil {
		t.Fatal(err)
	}
	kind, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if kind != websocket.TextMessage || string(data) != "ping" {
		t.Fatalf("message = %d %q", kind, data)
	}
}

func TestSyncSSHKeysSkipsUnavailableTunnelOnlyRegion(t *testing.T) {
	registry, err := Parse([]byte("regions:\n- name: eu\n  key: secret\n"))
	if err != nil {
		t.Fatal(err)
	}
	hub := NewHub(registry)
	problems := hub.SyncSSHKeys(context.Background(), "token", []string{"ssh-ed25519 AAAA test"}, Identity{User: auth.User{Username: "token"}, Admin: true})
	if len(problems) != 0 {
		t.Fatalf("problems = %v", problems)
	}
}
