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
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	RegionNameHeader     = "X-Poddy-Region-Name"
	RegionIdentityHeader = "X-Poddy-Region-Identity"
	RegionHTTPTarget     = "region-http"
)

type RegionAuthFunc func(region, token string) error

type RegionBroker struct {
	auth RegionAuthFunc

	mu       sync.RWMutex
	sessions map[string][]*session
	next     map[string]uint32

	upgrader websocket.Upgrader
}

func NewRegionBroker(auth RegionAuthFunc) *RegionBroker {
	return &RegionBroker{
		auth:     auth,
		sessions: map[string][]*session{},
		next:     map[string]uint32{},
		upgrader: websocket.Upgrader{
			HandshakeTimeout: 10 * time.Second,
			ReadBufferSize:   32 << 10,
			WriteBufferSize:  32 << 10,
			CheckOrigin:      func(*http.Request) bool { return true },
		},
	}
}

func (b *RegionBroker) Serve(w http.ResponseWriter, r *http.Request) error {
	if b == nil {
		http.Error(w, "region tunnel is not configured", http.StatusNotFound)
		return errors.New("region tunnel is not configured")
	}
	region := strings.TrimSpace(r.Header.Get(RegionNameHeader))
	token := strings.TrimSpace(r.Header.Get(RegionIdentityHeader))
	if region == "" || token == "" {
		http.Error(w, "missing region identity", http.StatusUnauthorized)
		return errors.New("missing region identity")
	}
	if b.auth != nil {
		if err := b.auth(region, token); err != nil {
			http.Error(w, "forbidden", http.StatusForbidden)
			return err
		}
	}
	conn, err := b.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return err
	}
	conn.SetReadLimit(maxFrameSize)

	s := newSession(conn, region)
	b.register(s)
	defer b.unregister(s)

	err = b.run(s)
	s.close(err)
	return err
}

func (b *RegionBroker) register(s *session) {
	b.mu.Lock()
	b.sessions[s.workspace] = append(b.sessions[s.workspace], s)
	count := len(b.sessions[s.workspace])
	b.mu.Unlock()
	log.Printf("region tunnel connected: %s (%d session(s))", s.workspace, count)
}

func (b *RegionBroker) unregister(s *session) {
	b.mu.Lock()
	list := b.sessions[s.workspace]
	for i, candidate := range list {
		if candidate == s {
			list = append(list[:i], list[i+1:]...)
			break
		}
	}
	if len(list) == 0 {
		delete(b.sessions, s.workspace)
		delete(b.next, s.workspace)
	} else {
		b.sessions[s.workspace] = list
	}
	b.mu.Unlock()
	log.Printf("region tunnel disconnected: %s", s.workspace)
}

func (b *RegionBroker) run(s *session) error {
	for {
		_, data, err := s.conn.ReadMessage()
		if err != nil {
			return err
		}
		kind, id, payload, err := decodeFrame(data)
		if err != nil {
			return err
		}
		switch kind {
		case frameOpened:
			s.resolveOpen(id, nil)
		case frameError:
			message := strings.TrimSpace(string(payload))
			if message == "" {
				message = "region rejected the tunnel"
			}
			s.resolveOpen(id, errors.New(message))
			if st, ok := s.stream(id); ok {
				st.closeWithError(errors.New(message), false)
			}
		case frameData:
			st, ok := s.stream(id)
			if !ok {
				continue
			}
			if !st.push(payload) {
				st.closeWithError(errors.New("tunnel stream buffer full"), true)
			}
		case frameClose:
			if st, ok := s.stream(id); ok {
				st.closeWithError(nil, false)
			}
		}
	}
}

func (b *RegionBroker) Connected(region string) bool {
	b.mu.RLock()
	list := b.sessions[region]
	b.mu.RUnlock()
	for _, s := range list {
		if s.alive() {
			return true
		}
	}
	return false
}

func (b *RegionBroker) Dial(ctx context.Context, region, target string) (net.Conn, error) {
	if b == nil {
		return nil, errors.New("region tunnel is not configured")
	}
	b.mu.Lock()
	list := b.sessions[region]
	var s *session
	if len(list) > 0 {
		start := int(b.next[region] % uint32(len(list)))
		for i := 0; i < len(list); i++ {
			candidate := list[(start+i)%len(list)]
			if candidate.alive() {
				s = candidate
				b.next[region] = uint32(start + i + 1)
				break
			}
		}
	}
	b.mu.Unlock()
	if s == nil {
		return nil, fmt.Errorf("region %s tunnel is not connected", region)
	}

	id := s.allocate()
	st := s.addStream(id)
	opened := s.waitOpen(id)
	if err := s.writeFrame(frameOpen, id, []byte(target)); err != nil {
		st.closeWithError(err, false)
		b.unregister(s)
		s.close(err)
		return nil, err
	}

	select {
	case result := <-opened:
		if result.err != nil {
			st.closeWithError(result.err, false)
			return nil, result.err
		}
		return st, nil
	case <-ctx.Done():
		st.closeWithError(ctx.Err(), true)
		return nil, ctx.Err()
	case <-s.done:
		st.closeWithError(errClosed, false)
		return nil, errClosed
	}
}

func (b *RegionBroker) Close() {
	b.mu.Lock()
	var sessions []*session
	for _, list := range b.sessions {
		sessions = append(sessions, list...)
	}
	b.sessions = map[string][]*session{}
	b.next = map[string]uint32{}
	b.mu.Unlock()
	for _, s := range sessions {
		s.close(errClosed)
	}
}

type RegionAgentConfig struct {
	URL      string
	Region   string
	Target   string
	Token    func() (string, error)
	Insecure bool
}

type RegionAgent struct {
	config RegionAgentConfig
	dialer websocket.Dialer
}

func NewRegionAgent(config RegionAgentConfig) *RegionAgent {
	dialer := websocket.Dialer{
		HandshakeTimeout: 15 * time.Second,
		Proxy:            http.ProxyFromEnvironment,
	}
	if config.Insecure {
		dialer.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	return &RegionAgent{config: config, dialer: dialer}
}

func (a *RegionAgent) Run(ctx context.Context) error {
	backoff := time.Second
	for {
		start := time.Now()
		err := a.connect(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			log.Printf("region tunnel: %v", err)
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

func (a *RegionAgent) connect(ctx context.Context) error {
	if strings.TrimSpace(a.config.Region) == "" {
		return errors.New("region name is required")
	}
	if strings.TrimSpace(a.config.Target) == "" {
		return errors.New("region tunnel target is required")
	}
	if a.config.Token == nil {
		return errors.New("region tunnel token is required")
	}
	endpoint, err := regionEndpoint(a.config.URL)
	if err != nil {
		return err
	}
	token, err := a.config.Token()
	if err != nil {
		return err
	}
	header := http.Header{}
	header.Set(RegionNameHeader, a.config.Region)
	header.Set(RegionIdentityHeader, token)
	conn, response, err := a.dialer.DialContext(ctx, endpoint, header)
	if err != nil {
		if response != nil {
			return fmt.Errorf("connect %s: %w (HTTP %d)", endpoint, err, response.StatusCode)
		}
		return fmt.Errorf("connect %s: %w", endpoint, err)
	}
	defer conn.Close()
	conn.SetReadLimit(maxFrameSize)
	log.Printf("region tunnel connected to %s as %s", endpoint, a.config.Region)

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
			if !a.allowedTarget(string(payload)) {
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
			remote, err := net.DialTimeout("tcp", a.config.Target, 10*time.Second)
			if err != nil {
				_ = writeFrame(frameError, id, []byte("cannot connect to region HTTP server"))
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
		}
	}
}

func (a *RegionAgent) allowedTarget(target string) bool {
	target = strings.TrimSpace(target)
	return target == "" || target == RegionHTTPTarget
}

func (a *RegionAgent) copyFromTarget(id uint32, target net.Conn, mu *sync.Mutex, targets map[uint32]net.Conn, writeFrame func(byte, uint32, []byte) error) {
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

func regionEndpoint(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("hub URL is required")
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
		return "", fmt.Errorf("unsupported hub URL scheme %q", parsed.Scheme)
	}
	if parsed.Host == "" {
		return "", errors.New("hub URL host is required")
	}
	const tunnelPath = "/api/internal/region/tunnel"
	if !strings.HasSuffix(parsed.Path, tunnelPath) {
		parsed.Path = strings.TrimRight(parsed.Path, "/") + tunnelPath
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}
