//go:build !linux

package netmon

import (
	"context"
	"errors"
	"time"
)

type Capture struct{}

func OpenCapture(string) (*Capture, error) {
	return nil, errors.New("packet capture is only supported on Linux")
}

func (c *Capture) Read(context.Context) ([]byte, time.Time, error) {
	return nil, time.Time{}, errors.New("packet capture is only supported on Linux")
}

func (c *Capture) Close() error { return nil }

func (c *Capture) Interface() string { return "" }
