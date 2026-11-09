package tunnel

import (
	"fmt"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type session struct {
	conn      *websocket.Conn
	workspace string

	writeMu sync.Mutex

	mu      sync.Mutex
	streams map[uint32]*stream
	pending map[uint32]chan openResult
	nextID  uint32

	done      chan struct{}
	closeOnce sync.Once
	expires   time.Time
}

func newSession(conn *websocket.Conn, workspace string) *session {
	return &session{
		conn:      conn,
		workspace: workspace,
		streams:   map[uint32]*stream{},
		pending:   map[uint32]chan openResult{},
		done:      make(chan struct{}),
	}
}

func (s *session) writeFrame(kind byte, id uint32, payload []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	select {
	case <-s.done:
		return errClosed
	default:
	}
	return s.conn.WriteMessage(websocket.BinaryMessage, encodeFrame(kind, id, payload))
}

func (s *session) addStream(id uint32) *stream {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := newStream(s, id)
	s.streams[id] = st
	return st
}

func (s *session) removeStream(id uint32) {
	s.mu.Lock()
	delete(s.streams, id)
	s.mu.Unlock()
}

func (s *session) stream(id uint32) (*stream, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.streams[id]
	return st, ok
}

func (s *session) waitOpen(id uint32) chan openResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch := make(chan openResult, 1)
	s.pending[id] = ch
	return ch
}

func (s *session) resolveOpen(id uint32, err error) {
	s.mu.Lock()
	ch := s.pending[id]
	delete(s.pending, id)
	s.mu.Unlock()
	if ch != nil {
		ch <- openResult{err: err}
	}
}

func (s *session) allocate() uint32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	return s.nextID
}

func (s *session) close(err error) {
	s.closeOnce.Do(func() {
		close(s.done)
		_ = s.conn.Close()

		s.mu.Lock()
		streams := make([]*stream, 0, len(s.streams))
		for _, st := range s.streams {
			streams = append(streams, st)
		}
		pending := make([]chan openResult, 0, len(s.pending))
		for _, ch := range s.pending {
			pending = append(pending, ch)
		}
		s.pending = map[uint32]chan openResult{}
		s.mu.Unlock()

		for _, st := range streams {
			st.closeWithError(err, false)
		}
		for _, ch := range pending {
			ch <- openResult{err: err}
		}
	})
}

func (s *session) expired() error {
	if s.expires.IsZero() {
		return nil
	}
	if time.Now().After(s.expires) {
		return fmt.Errorf("tunnel token expired")
	}
	return nil
}
