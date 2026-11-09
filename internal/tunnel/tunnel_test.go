package tunnel

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestTokenSigner(t *testing.T) {
	signer := NewTokenSigner("secret")
	token, expires, err := signer.SignAgent("demo", "alice@example.com", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if time.Until(expires) <= 0 {
		t.Fatal("token is already expired")
	}
	claims, err := signer.VerifyAgent(token)
	if err != nil {
		t.Fatal(err)
	}
	if claims.Workspace != "demo" || claims.Owner != "alice@example.com" {
		t.Fatalf("claims = %+v", claims)
	}
	if _, err := NewTokenSigner("other").VerifyAgent(token); err == nil {
		t.Fatal("token verified with the wrong key")
	}
	if _, err := signer.VerifyAgent("garbage"); err == nil {
		t.Fatal("garbage token verified")
	}
}

func TestAgentEndpoint(t *testing.T) {
	tests := map[string]string{
		"http://portal:8091":                           "ws://portal:8091/api/internal/agent/tunnel",
		"https://portal":                               "wss://portal/api/internal/agent/tunnel",
		"ws://portal/base":                             "ws://portal/base/api/internal/agent/tunnel",
		"http://portal:8091/":                          "ws://portal:8091/api/internal/agent/tunnel",
		"http://portal:8091/api/internal/agent/tunnel": "ws://portal:8091/api/internal/agent/tunnel",
	}
	for input, want := range tests {
		got, err := agentEndpoint(input)
		if err != nil {
			t.Fatalf("%s: %v", input, err)
		}
		if got != want {
			t.Fatalf("%s: got %q want %q", input, got, want)
		}
	}
	if _, err := agentEndpoint("ftp://portal"); err == nil {
		t.Fatal("unsupported scheme accepted")
	}
}

func TestBrokerAgentRoundTrip(t *testing.T) {
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		for {
			conn, err := echo.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_, _ = io.Copy(conn, conn)
			}()
		}
	}()

	signer := NewTokenSigner("secret")
	broker := NewBroker(signer)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, err := broker.Authenticate(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		_ = broker.ServeAgent(w, r, claims)
	}))
	defer server.Close()
	defer broker.Close()

	token, _, err := signer.SignAgent("demo", "alice@example.com", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	agent := NewAgent(AgentConfig{
		URL:       server.URL,
		Token:     token,
		Workspace: "demo",
		AllowedTarget: func(string) bool {
			return true
		},
	})
	go func() { _ = agent.Run(ctx) }()

	deadline := time.Now().Add(3 * time.Second)
	for !broker.Connected("demo") {
		if time.Now().After(deadline) {
			t.Fatal("agent did not connect")
		}
		time.Sleep(10 * time.Millisecond)
	}

	conn, err := broker.Dial(ctx, "demo", echo.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	if _, err := conn.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 5)
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "hello" {
		t.Fatalf("got %q", buf)
	}
}

func TestAgentRejectsTargetsOutsideLoopbackPort22(t *testing.T) {
	agent := NewAgent(AgentConfig{})
	for _, target := range []string{"example.com:22", "127.0.0.1:3000", "10.0.0.1:22", "127.0.0.1"} {
		if agent.allowed(target) {
			t.Fatalf("target %q should be rejected", target)
		}
	}
	for _, target := range []string{"127.0.0.1:22", "localhost:22", "[::1]:22"} {
		if !agent.allowed(target) {
			t.Fatalf("target %q should be allowed", target)
		}
	}
}
