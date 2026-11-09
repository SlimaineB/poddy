package tunnel

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type Broker struct {
	signer *TokenSigner

	mu     sync.RWMutex
	agents map[string]*session

	upgrader websocket.Upgrader
}

func NewBroker(signer *TokenSigner) *Broker {
	return &Broker{
		signer: signer,
		agents: map[string]*session{},
		upgrader: websocket.Upgrader{
			HandshakeTimeout: 10 * time.Second,
			ReadBufferSize:   32 << 10,
			WriteBufferSize:  32 << 10,
			CheckOrigin:      func(*http.Request) bool { return true },
		},
	}
}

func (b *Broker) Authenticate(r *http.Request) (AgentClaims, error) {
	if b == nil || b.signer == nil {
		return AgentClaims{}, errors.New("tunnel is not configured")
	}
	token := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if token == "" {
		token = strings.TrimSpace(r.Header.Get("X-Poddy-Agent-Token"))
	}
	if token == "" {
		return AgentClaims{}, errors.New("missing agent token")
	}
	return b.signer.VerifyAgent(token)
}

func (b *Broker) ServeAgent(w http.ResponseWriter, r *http.Request, claims AgentClaims) error {
	if b == nil {
		return errors.New("tunnel is not configured")
	}
	if claims.Expires <= time.Now().Unix() {
		return errors.New("agent token expired")
	}
	conn, err := b.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return err
	}
	conn.SetReadLimit(maxFrameSize)

	s := newSession(conn, claims.Workspace)
	s.expires = time.Unix(claims.Expires, 0)
	b.register(s)

	timer := time.AfterFunc(time.Until(s.expires), func() {
		s.close(errors.New("agent token expired"))
	})
	defer timer.Stop()

	err = b.run(s)
	s.close(err)
	b.unregister(s)
	return err
}

func (b *Broker) register(s *session) {
	b.mu.Lock()
	previous := b.agents[s.workspace]
	b.agents[s.workspace] = s
	b.mu.Unlock()
	if previous != nil && previous != s {
		previous.close(errors.New("agent reconnected"))
	}
}

func (b *Broker) unregister(s *session) {
	b.mu.Lock()
	if b.agents[s.workspace] == s {
		delete(b.agents, s.workspace)
	}
	b.mu.Unlock()
}

func (b *Broker) run(s *session) error {
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
				message = "agent rejected the tunnel"
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
				st.closeWithError(io.EOF, false)
			}
		}
	}
}

func (b *Broker) Connected(workspace string) bool {
	b.mu.RLock()
	s := b.agents[workspace]
	b.mu.RUnlock()
	if s == nil {
		return false
	}
	select {
	case <-s.done:
		return false
	default:
	}
	return s.expired() == nil
}

func (b *Broker) Dial(ctx context.Context, workspace, target string) (net.Conn, error) {
	if b == nil {
		return nil, errors.New("tunnel is not configured")
	}
	b.mu.RLock()
	s := b.agents[workspace]
	b.mu.RUnlock()
	if s == nil {
		return nil, fmt.Errorf("workspace agent is not connected")
	}
	if err := s.expired(); err != nil {
		return nil, err
	}

	id := s.allocate()
	st := s.addStream(id)
	opened := s.waitOpen(id)
	if err := s.writeFrame(frameOpen, id, []byte(target)); err != nil {
		st.closeWithError(err, false)
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

func (b *Broker) Close() {
	b.mu.Lock()
	agents := make([]*session, 0, len(b.agents))
	for _, s := range b.agents {
		agents = append(agents, s)
	}
	b.agents = map[string]*session{}
	b.mu.Unlock()
	for _, s := range agents {
		s.close(errClosed)
	}
}
