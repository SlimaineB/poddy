package tunnel

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// DialStatusError reports an HTTP status returned while opening a tunnel
// connection. Callers use it to distinguish "not found" from transient errors.
type DialStatusError struct {
	StatusCode int
	Message    string
	Err        error
}

func (e *DialStatusError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("tunnel dial failed: %s (HTTP %d)", e.Message, e.StatusCode)
	}
	if e.Err != nil {
		return fmt.Sprintf("tunnel dial failed: %v (HTTP %d)", e.Err, e.StatusCode)
	}
	return fmt.Sprintf("tunnel dial failed (HTTP %d)", e.StatusCode)
}

func (e *DialStatusError) Unwrap() error { return e.Err }

// AcceptConn upgrades an HTTP request to a WebSocket and exposes it as a
// net.Conn. Each binary message carries raw bytes; there is no extra framing.
func AcceptConn(w http.ResponseWriter, r *http.Request) (net.Conn, error) {
	upgrader := websocket.Upgrader{
		HandshakeTimeout: 10 * time.Second,
		ReadBufferSize:   32 << 10,
		WriteBufferSize:  32 << 10,
		CheckOrigin:      func(*http.Request) bool { return true },
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return nil, err
	}
	return newWSConn(conn), nil
}

// DialConn opens a WebSocket and exposes it as a net.Conn.
func DialConn(ctx context.Context, endpoint string, header http.Header) (net.Conn, error) {
	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
		Proxy:            http.ProxyFromEnvironment,
		ReadBufferSize:   32 << 10,
		WriteBufferSize:  32 << 10,
	}
	conn, response, err := dialer.DialContext(ctx, endpoint, header)
	if err != nil {
		if response != nil {
			message := ""
			if response.Body != nil {
				body, _ := io.ReadAll(io.LimitReader(response.Body, 512))
				_ = response.Body.Close()
				message = string(body)
			}
			return nil, &DialStatusError{StatusCode: response.StatusCode, Message: message, Err: err}
		}
		return nil, err
	}
	return newWSConn(conn), nil
}

type wsConn struct {
	conn *websocket.Conn

	readMu sync.Mutex
	buf    []byte

	writeMu   sync.Mutex
	closeOnce sync.Once
}

func newWSConn(conn *websocket.Conn) *wsConn {
	conn.SetReadLimit(maxFrameSize)
	return &wsConn{conn: conn}
}

func (c *wsConn) Read(p []byte) (int, error) {
	c.readMu.Lock()
	defer c.readMu.Unlock()
	for len(c.buf) == 0 {
		kind, data, err := c.conn.ReadMessage()
		if err != nil {
			return 0, err
		}
		if kind != websocket.BinaryMessage {
			continue
		}
		c.buf = data
	}
	n := copy(p, c.buf)
	c.buf = c.buf[n:]
	return n, nil
}

func (c *wsConn) Write(p []byte) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := c.conn.WriteMessage(websocket.BinaryMessage, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *wsConn) Close() error {
	var err error
	c.closeOnce.Do(func() {
		c.writeMu.Lock()
		deadline := time.Now().Add(time.Second)
		_ = c.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), deadline)
		c.writeMu.Unlock()
		err = c.conn.Close()
	})
	return err
}

func (c *wsConn) LocalAddr() net.Addr  { return netAddr("websocket") }
func (c *wsConn) RemoteAddr() net.Addr { return netAddr(c.conn.RemoteAddr().String()) }

func (c *wsConn) SetDeadline(time.Time) error      { return nil }
func (c *wsConn) SetReadDeadline(time.Time) error  { return nil }
func (c *wsConn) SetWriteDeadline(time.Time) error { return nil }
