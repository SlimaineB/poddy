package tunnel

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const defaultAgentTarget = "127.0.0.1:22"

type AgentConfig struct {
	URL         string
	Token       string
	TokenFile   string
	RefreshFile string
	Workspace   string
	Target      string
	Insecure    bool
	// AllowedTarget overrides the target validator. It is mainly useful for
	// tests; production agents only accept loopback port 22.
	AllowedTarget func(string) bool
}

type Agent struct {
	config AgentConfig
	dialer websocket.Dialer

	tokenMu sync.RWMutex
	token   string
}

func NewAgent(config AgentConfig) *Agent {
	if strings.TrimSpace(config.Target) == "" {
		config.Target = defaultAgentTarget
	}
	dialer := websocket.Dialer{
		HandshakeTimeout: 15 * time.Second,
		Proxy:            http.ProxyFromEnvironment,
	}
	if config.Insecure {
		// #nosec G402 -- Explicit opt-in for self-signed tunnel endpoints.
		dialer.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	token := freshestToken(config.Token, readTokenFile(config.TokenFile), readTokenFile(config.RefreshFile))
	return &Agent{config: config, dialer: dialer, token: token}
}

func (a *Agent) Run(ctx context.Context) error {
	backoff := time.Second
	for {
		start := time.Now()
		err := a.connect(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			log.Printf("workspace agent: %v", err)
		}
		if time.Since(start) > 30*time.Second {
			backoff = time.Second
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func (a *Agent) connect(ctx context.Context) error {
	a.refreshToken()
	endpoint, err := agentEndpoint(a.config.URL)
	if err != nil {
		return err
	}
	header := http.Header{}
	header.Set("Authorization", "Bearer "+strings.TrimSpace(a.currentToken()))
	if a.config.Workspace != "" {
		header.Set("X-Poddy-Agent-Workspace", a.config.Workspace)
	}
	conn, response, err := a.dialer.DialContext(ctx, endpoint, header)
	if err != nil {
		if response != nil {
			return fmt.Errorf("connect %s: %w (HTTP %d)", endpoint, err, response.StatusCode)
		}
		return fmt.Errorf("connect %s: %w", endpoint, err)
	}
	defer conn.Close()
	conn.SetReadLimit(maxFrameSize)

	targets := map[uint32]net.Conn{}
	var mu sync.Mutex
	var writeMu sync.Mutex
	writeFrame := func(kind byte, id uint32, payload []byte) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return conn.WriteMessage(websocket.BinaryMessage, encodeFrame(kind, id, payload))
	}
	defer func() {
		mu.Lock()
		for _, target := range targets {
			_ = target.Close()
		}
		mu.Unlock()
	}()

	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		kind, id, payload, err := decodeFrame(data)
		if err != nil {
			return err
		}
		switch kind {
		case frameOpen:
			target := strings.TrimSpace(string(payload))
			if !a.allowed(target) {
				_ = writeFrame(frameError, id, []byte("target is not allowed"))
				continue
			}
			mu.Lock()
			existing := targets[id]
			mu.Unlock()
			if existing != nil {
				_ = writeFrame(frameError, id, []byte("stream already exists"))
				continue
			}
			remote, err := net.DialTimeout("tcp", target, 10*time.Second)
			if err != nil {
				_ = writeFrame(frameError, id, []byte("cannot connect to workspace service"))
				continue
			}
			mu.Lock()
			targets[id] = remote
			mu.Unlock()
			if err := writeFrame(frameOpened, id, nil); err != nil {
				_ = remote.Close()
				return err
			}
			go a.copyFromTarget(id, remote, &mu, targets, writeFrame)
		case frameData:
			mu.Lock()
			target := targets[id]
			mu.Unlock()
			if target == nil {
				continue
			}
			if _, err := target.Write(payload); err != nil {
				_ = target.Close()
				mu.Lock()
				delete(targets, id)
				mu.Unlock()
				_ = writeFrame(frameClose, id, nil)
			}
		case frameClose:
			mu.Lock()
			target := targets[id]
			delete(targets, id)
			mu.Unlock()
			if target != nil {
				_ = target.Close()
			}
		case frameToken:
			if token := strings.TrimSpace(string(payload)); token != "" {
				a.setToken(token)
			}
		}
	}
}

func readTokenFile(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	raw, err := os.ReadFile(path) // #nosec G304 -- Path is supplied by the pod template.
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

func freshestToken(candidates ...string) string {
	best := ""
	var bestExpiry time.Time
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		expiry := tokenExpiry(candidate)
		if best == "" || expiry.After(bestExpiry) {
			best = candidate
			bestExpiry = expiry
		}
	}
	return best
}

func (a *Agent) refreshToken() {
	token := readTokenFile(a.config.RefreshFile)
	if token == "" {
		return
	}
	if freshestToken(a.currentToken(), token) != token {
		return
	}
	a.setToken(token)
}

func (a *Agent) currentToken() string {
	a.tokenMu.RLock()
	defer a.tokenMu.RUnlock()
	return a.token
}

func (a *Agent) setToken(token string) {
	a.tokenMu.Lock()
	a.token = token
	a.tokenMu.Unlock()
	if path := strings.TrimSpace(a.config.TokenFile); path != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err == nil {
			_ = os.WriteFile(path, []byte(token), 0o600)
		}
	}
}

func (a *Agent) copyFromTarget(id uint32, target net.Conn, mu *sync.Mutex, targets map[uint32]net.Conn, writeFrame func(byte, uint32, []byte) error) {
	buf := make([]byte, 32<<10)
	for {
		n, err := target.Read(buf)
		if n > 0 {
			if writeErr := writeFrame(frameData, id, buf[:n]); writeErr != nil {
				return
			}
		}
		if err != nil {
			mu.Lock()
			if targets[id] == target {
				delete(targets, id)
			}
			mu.Unlock()
			_ = target.Close()
			_ = writeFrame(frameClose, id, nil)
			return
		}
	}
}

func (a *Agent) allowed(target string) bool {
	if a.config.AllowedTarget != nil {
		return a.config.AllowedTarget(target)
	}
	host, port, err := net.SplitHostPort(target)
	if err != nil || port != "22" {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func agentEndpoint(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("agent URL is required")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	switch parsed.Scheme {
	case "http":
		parsed.Scheme = "ws"
	case "https":
		parsed.Scheme = "wss"
	case "ws", "wss":
	default:
		return "", fmt.Errorf("unsupported agent URL scheme %q", parsed.Scheme)
	}
	if parsed.Host == "" {
		return "", errors.New("agent URL host is required")
	}
	const tunnelPath = "/api/internal/agent/tunnel"
	if !strings.HasSuffix(parsed.Path, tunnelPath) {
		parsed.Path = strings.TrimRight(parsed.Path, "/") + tunnelPath
	}
	return parsed.String(), nil
}
