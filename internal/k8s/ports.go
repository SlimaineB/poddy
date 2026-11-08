package k8s

import (
	"context"
	"encoding/hex"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Port describes a TCP port listening inside a workspace.
type Port struct {
	Port     int    `json:"port"`
	Address  string `json:"address,omitempty"`
	Protocol string `json:"protocol,omitempty"`
}

// ListeningPorts reads the TCP tables of the workspace network namespace. It
// avoids depending on tools such as ss or netstat, which are not present in
// every workspace image.
func (m *Manager) ListeningPorts(ctx context.Context, name string) ([]Port, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	stdout, stderr, err := m.Exec(ctx, name, []string{
		"/bin/sh", "-c", "cat /proc/net/tcp /proc/net/tcp6 2>/dev/null",
	})
	if err != nil {
		message := strings.TrimSpace(string(stderr))
		if message != "" {
			return nil, fmt.Errorf("%w: %s", err, message)
		}
		return nil, err
	}
	return parseProcNetTCP(stdout), nil
}

func parseProcNetTCP(data []byte) []Port {
	ports := map[int]Port{}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		// sl local_address rem_address st ...
		if len(fields) < 4 || fields[3] != "0A" {
			continue
		}
		addressHex, portHex, ok := strings.Cut(fields[1], ":")
		if !ok {
			continue
		}
		value, err := strconv.ParseUint(portHex, 16, 16)
		if err != nil || value == 0 || value == 22 {
			continue
		}
		port := Port{
			Port:     int(value),
			Address:  parseProcAddress(addressHex),
			Protocol: "tcp",
		}
		existing, exists := ports[port.Port]
		if !exists || isWildcard(port.Address) || (!isWildcard(existing.Address) && isLoopback(existing.Address)) {
			ports[port.Port] = port
		}
	}

	out := make([]Port, 0, len(ports))
	for _, port := range ports {
		out = append(out, port)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Port < out[j].Port })
	return out
}

func parseProcAddress(value string) string {
	raw, err := hex.DecodeString(value)
	if err != nil {
		return ""
	}
	switch len(raw) {
	case net.IPv4len:
		reverseBytes(raw)
		return net.IP(raw).String()
	case net.IPv6len:
		for offset := 0; offset < len(raw); offset += 4 {
			reverseBytes(raw[offset : offset+4])
		}
		return net.IP(raw).String()
	default:
		return ""
	}
}

func reverseBytes(value []byte) {
	for left, right := 0, len(value)-1; left < right; left, right = left+1, right-1 {
		value[left], value[right] = value[right], value[left]
	}
}

func isWildcard(address string) bool {
	return address == "0.0.0.0" || address == "::"
}

func isLoopback(address string) bool {
	ip := net.ParseIP(address)
	return ip != nil && ip.IsLoopback()
}
