package tunnel

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRegionTunnelHTTP(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "region-ok")
	}))
	defer target.Close()

	broker := NewRegionBroker(func(region, token string) error {
		if region != "eu" || token != "token" {
			return errors.New("bad region identity")
		}
		return nil
	})
	connected := make(chan string, 1)
	broker.SetOnConnect(func(region string) { connected <- region })
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = broker.Serve(w, r)
	}))
	defer hub.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	agent := NewRegionAgent(RegionAgentConfig{
		URL:    hub.URL,
		Region: "eu",
		Target: target.Listener.Addr().String(),
		Token:  func() (string, error) { return "token", nil },
	})
	go func() { _ = agent.Run(ctx) }()

	deadline := time.Now().Add(3 * time.Second)
	for !broker.Connected("eu") {
		if time.Now().After(deadline) {
			t.Fatal("region tunnel did not connect")
		}
		time.Sleep(10 * time.Millisecond)
	}
	select {
	case region := <-connected:
		if region != "eu" {
			t.Fatalf("connected region = %q", region)
		}
	case <-time.After(time.Second):
		t.Fatal("connect callback was not called")
	}

	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return broker.Dial(ctx, "eu", RegionHTTPTarget)
		},
	}}
	resp, err := client.Get("http://region.invalid/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(body)) != "region-ok" {
		t.Fatalf("body = %q", body)
	}
}

func TestWorkspaceTarget(t *testing.T) {
	if got := WorkspaceTarget("app", 22); got != "tcp:app:22" {
		t.Fatalf("WorkspaceTarget = %q", got)
	}
	cases := []struct {
		target    string
		workspace string
		port      int
		ok        bool
	}{
		{"tcp:app:22", "app", 22, true},
		{"tcp:app:2222", "app", 2222, true},
		{"tcp::22", "", 0, false},
		{"tcp:app", "", 0, false},
		{"tcp:app:0", "", 0, false},
		{"tcp:app:99999", "", 0, false},
		{"http:app:22", "", 0, false},
		{"region-http", "", 0, false},
	}
	for _, tc := range cases {
		workspace, port, ok := parseWorkspaceTarget(tc.target)
		if ok != tc.ok || workspace != tc.workspace || port != tc.port {
			t.Errorf("parseWorkspaceTarget(%q) = %q, %d, %v", tc.target, workspace, port, ok)
		}
	}
}

func TestRegionTunnelWorkspaceTarget(t *testing.T) {
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		conn, err := echo.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = io.Copy(conn, conn)
	}()

	broker := NewRegionBroker(func(region, token string) error { return nil })
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = broker.Serve(w, r)
	}))
	defer hub.Close()

	type dialed struct {
		workspace string
		port      int
	}
	dials := make(chan dialed, 1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	agent := NewRegionAgent(RegionAgentConfig{
		URL:    hub.URL,
		Region: "eu",
		Target: "127.0.0.1:1",
		DialWorkspace: func(ctx context.Context, workspace string, port int) (net.Conn, error) {
			dials <- dialed{workspace: workspace, port: port}
			var dialer net.Dialer
			return dialer.DialContext(ctx, "tcp", echo.Addr().String())
		},
		Token: func() (string, error) { return "token", nil },
	})
	go func() { _ = agent.Run(ctx) }()

	deadline := time.Now().Add(3 * time.Second)
	for !broker.Connected("eu") {
		if time.Now().After(deadline) {
			t.Fatal("region tunnel did not connect")
		}
		time.Sleep(10 * time.Millisecond)
	}

	dialCtx, dialCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer dialCancel()
	conn, err := broker.Dial(dialCtx, "eu", WorkspaceTarget("demo", 22))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "ping" {
		t.Fatalf("echo = %q", buf)
	}
	select {
	case got := <-dials:
		if got.workspace != "demo" || got.port != 22 {
			t.Fatalf("dialed = %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("DialWorkspace was not called")
	}
}
