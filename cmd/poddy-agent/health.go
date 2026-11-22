package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type agentHealth struct {
	mu     sync.RWMutex
	ready  bool
	reason string
}

func newAgentHealth() *agentHealth {
	return &agentHealth{reason: "starting"}
}

func (h *agentHealth) setReady(ready bool, reason string) {
	if h == nil {
		return
	}
	h.mu.Lock()
	h.ready = ready
	if strings.TrimSpace(reason) == "" {
		h.reason = "ready"
	} else {
		h.reason = reason
	}
	h.mu.Unlock()
}

func (h *agentHealth) status() (bool, string) {
	if h == nil {
		return true, ""
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.ready, h.reason
}

func (h *agentHealth) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		ready, reason := h.status()
		if ready {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = fmt.Fprintf(w, "not ready: %s\n", reason)
	})
	return mux
}

func (h *agentHealth) run(ctx context.Context, addr string) error {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return nil
	}
	server := &http.Server{
		Addr:              addr,
		Handler:           h.handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
