package bastion

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"testing"
	"time"

	"poddy/internal/sshkeys"
)

func TestConnectionLimiter(t *testing.T) {
	b := &Bastion{MaxConnections: 2, MaxConnectionsPerIP: 1, perIP: map[string]int{}}
	if !b.acquire("127.0.0.1") {
		t.Fatal("first connection rejected")
	}
	if b.acquire("127.0.0.1") {
		t.Fatal("second connection from the same IP accepted")
	}
	if !b.acquire("127.0.0.2") {
		t.Fatal("connection from another IP rejected")
	}
	if b.acquire("127.0.0.3") {
		t.Fatal("global connection limit not enforced")
	}
	b.release("127.0.0.1")
	if !b.acquire("127.0.0.1") {
		t.Fatal("connection rejected after release")
	}
}

func TestWorkspaceName(t *testing.T) {
	tests := map[string]string{
		"poddy-ssh-demo":       "demo",
		"poddy-demo":           "demo",
		"[poddy-ssh-demo]":     "demo",
		"[poddy-ssh-demo]:22":  "demo",
		"poddy-ssh-demo:22":    "demo",
		"  [poddy-ssh-demo]  ": "demo",
	}
	for host, want := range tests {
		if got := workspaceName(host); got != want {
			t.Errorf("workspaceName(%q) = %q, want %q", host, got, want)
		}
	}
}

func TestDrainWaitsForActiveConnections(t *testing.T) {
	keys, err := sshkeys.Parse(nil, true)
	if err != nil {
		t.Fatal(err)
	}
	b, err := New("127.0.0.1:0", nil, keys, filepath.Join(t.TempDir(), "host-key"))
	if err != nil {
		t.Fatal(err)
	}
	b.DrainDelay = 0
	if err := b.Listen(); err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	go func() { _ = b.Serve() }()

	conn, err := net.Dial("tcp", b.listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	deadline := time.Now().Add(time.Second)
	for b.activeConnections() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("connection was not tracked")
		}
		time.Sleep(time.Millisecond)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := b.Drain(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Drain() = %v, want deadline exceeded", err)
	}
}

func TestDrainStopsListener(t *testing.T) {
	keys, err := sshkeys.Parse(nil, true)
	if err != nil {
		t.Fatal(err)
	}
	b, err := New("127.0.0.1:0", nil, keys, filepath.Join(t.TempDir(), "host-key"))
	if err != nil {
		t.Fatal(err)
	}
	b.DrainDelay = 0
	if err := b.Listen(); err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	addr := b.listener.Addr().String()
	go func() { _ = b.Serve() }()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := b.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	if conn, err := net.DialTimeout("tcp", addr, 50*time.Millisecond); err == nil {
		_ = conn.Close()
		t.Fatal("Drain left the SSH listener open")
	}
}
