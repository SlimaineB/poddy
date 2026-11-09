package tunnel

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

const (
	frameOpen   byte = 1
	frameOpened byte = 2
	frameData   byte = 3
	frameClose  byte = 4
	frameError  byte = 5

	frameHeaderSize = 5
	maxFrameSize    = 1 << 20
)

var (
	errClosed     = errors.New("tunnel is closed")
	errFrameLarge = errors.New("tunnel frame is too large")
)

func encodeFrame(kind byte, id uint32, payload []byte) []byte {
	frame := make([]byte, frameHeaderSize+len(payload))
	frame[0] = kind
	binary.BigEndian.PutUint32(frame[1:frameHeaderSize], id)
	copy(frame[frameHeaderSize:], payload)
	return frame
}

func decodeFrame(data []byte) (byte, uint32, []byte, error) {
	if len(data) < frameHeaderSize {
		return 0, 0, nil, fmt.Errorf("tunnel frame is too short")
	}
	if len(data) > maxFrameSize {
		return 0, 0, nil, errFrameLarge
	}
	return data[0], binary.BigEndian.Uint32(data[1:frameHeaderSize]), data[frameHeaderSize:], nil
}

type openResult struct {
	err error
}

type stream struct {
	session *session
	id      uint32

	mu     sync.Mutex
	closed bool
	input  chan []byte
	pipeR  *io.PipeReader
	pipeW  *io.PipeWriter

	closeOnce sync.Once
}

func newStream(s *session, id uint32) *stream {
	pr, pw := io.Pipe()
	st := &stream{
		session: s,
		id:      id,
		input:   make(chan []byte, 128),
		pipeR:   pr,
		pipeW:   pw,
	}
	go st.pump()
	return st
}

func (s *stream) pump() {
	defer s.pipeW.Close()
	for chunk := range s.input {
		if _, err := s.pipeW.Write(chunk); err != nil {
			return
		}
	}
}

func (s *stream) push(chunk []byte) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	if len(chunk) == 0 {
		return true
	}
	select {
	case s.input <- chunk:
		return true
	default:
		return false
	}
}

func (s *stream) Read(p []byte) (int, error) {
	return s.pipeR.Read(p)
}

func (s *stream) Write(p []byte) (int, error) {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return 0, io.ErrClosedPipe
	}
	if err := s.session.writeFrame(frameData, s.id, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (s *stream) Close() error {
	s.closeWithError(nil, true)
	return nil
}

func (s *stream) closeWithError(err error, notify bool) {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		close(s.input)
		s.mu.Unlock()

		if err != nil {
			_ = s.pipeW.CloseWithError(err)
		}
		s.session.removeStream(s.id)
		if notify {
			_ = s.session.writeFrame(frameClose, s.id, nil)
		}
	})
}

func (s *stream) LocalAddr() net.Addr { return netAddr("tunnel") }
func (s *stream) RemoteAddr() net.Addr {
	return netAddr(s.session.workspace)
}

func (s *stream) SetDeadline(time.Time) error      { return nil }
func (s *stream) SetReadDeadline(time.Time) error  { return nil }
func (s *stream) SetWriteDeadline(time.Time) error { return nil }

type netAddr string

func (a netAddr) Network() string { return "tunnel" }
func (a netAddr) String() string  { return string(a) }
