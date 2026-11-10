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
