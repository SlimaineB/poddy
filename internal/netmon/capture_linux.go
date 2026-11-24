//go:build linux

package netmon

import (
	"context"
	"fmt"
	"net"
	"time"

	"golang.org/x/sys/unix"
)

const ethPAll = 0x0003

type Capture struct {
	fd    int
	buf   []byte
	iface string
}

func OpenCapture(iface string) (*Capture, error) {
	fd, err := unix.Socket(unix.AF_PACKET, unix.SOCK_RAW|unix.SOCK_CLOEXEC, int(htons(ethPAll)))
	if err != nil {
		return nil, fmt.Errorf("packet socket: %w", err)
	}
	_ = unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_RCVBUF, 8<<20)
	_ = unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_RCVBUFFORCE, 32<<20)

	link := &unix.SockaddrLinklayer{Protocol: htons(ethPAll)}
	if iface != "" {
		device, err := net.InterfaceByName(iface)
		if err != nil {
			_ = unix.Close(fd)
			return nil, fmt.Errorf("capture interface %q: %w", iface, err)
		}
		link.Ifindex = device.Index
	}
	if err := unix.Bind(fd, link); err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("bind packet socket: %w", err)
	}
	return &Capture{fd: fd, buf: make([]byte, 256<<10), iface: iface}, nil
}

func (c *Capture) Read(ctx context.Context) ([]byte, time.Time, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, time.Time{}, err
		}
		poll := []unix.PollFd{{Fd: int32(c.fd), Events: unix.POLLIN}}
		n, err := unix.Poll(poll, 500)
		if err != nil {
			if err == unix.EINTR {
				continue
			}
			return nil, time.Time{}, err
		}
		if n == 0 {
			continue
		}
		read, _, err := unix.Recvfrom(c.fd, c.buf, unix.MSG_DONTWAIT)
		if err != nil {
			if err == unix.EAGAIN || err == unix.EWOULDBLOCK || err == unix.EINTR {
				continue
			}
			return nil, time.Time{}, err
		}
		if read == 0 {
			continue
		}
		return c.buf[:read], time.Now(), nil
	}
}

func (c *Capture) Close() error {
	if c == nil || c.fd < 0 {
		return nil
	}
	err := unix.Close(c.fd)
	c.fd = -1
	return err
}

func (c *Capture) Interface() string {
	if c == nil {
		return ""
	}
	return c.iface
}

func htons(value uint16) uint16 {
	return value<<8 | value>>8
}
