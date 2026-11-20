package tunnel

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWSConnRoundTrip(t *testing.T) {
	received := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received <- r.Header.Get(RegionSSHCertHeader)
		conn, err := AcceptConn(w, r)
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = io.Copy(conn, conn)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	header := http.Header{}
	header.Set(RegionSSHCertHeader, "cert")
	conn, err := DialConn(ctx, strings.Replace(srv.URL, "http://", "ws://", 1), header)
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
		t.Fatalf("echo = %q", buf)
	}
	select {
	case got := <-received:
		if got != "cert" {
			t.Fatalf("header = %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not receive header")
	}
}

func TestDialConnStatusError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "workspace not found", http.StatusNotFound)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := DialConn(ctx, strings.Replace(srv.URL, "http://", "ws://", 1), nil)
	var statusErr *DialStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("error = %v", err)
	}
	if statusErr.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d", statusErr.StatusCode)
	}
	if !strings.Contains(statusErr.Message, "workspace not found") {
		t.Fatalf("message = %q", statusErr.Message)
	}
}
