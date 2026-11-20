package tunnel

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	KindWorkspace = "workspace"
	KindRegion    = "region"

	presenceInterval = 10 * time.Second
)

// Presence records which portal replica currently holds a live tunnel, so any
// replica can forward an SSH stream to the owner over the cluster network.
type Presence interface {
	Claim(ctx context.Context, kind, name, address string) error
	Release(ctx context.Context, kind, name string) error
	Lookup(ctx context.Context, kind, name string) (address string, ok bool, err error)
}

// PeerDialer resolves a workspace through a local tunnel, a peer replica, or a
// region manager. It never accepts a connection from outside the cluster.
type PeerDialer struct {
	Broker       *Broker
	RegionTunnel *RegionBroker
	RegionNames  []string
	Presence     Presence
	Address      string
	Key          []byte
}

func (d *PeerDialer) DialWorkspace(ctx context.Context, workspace string, port int) (net.Conn, error) {
	if d == nil {
		return nil, errors.New("tunnel is not configured")
	}
	target := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	if d.Broker != nil && d.Broker.Connected(workspace) {
		return d.Broker.Dial(ctx, workspace, target)
	}
	if conn, err := d.dialPeer(ctx, KindWorkspace, workspace, target); err == nil {
		return conn, nil
	}
	return nil, fmt.Errorf("workspace %q is not reachable", workspace)
}

func (d *PeerDialer) DialRegionWorkspace(ctx context.Context, workspace string, port int) (net.Conn, error) {
	if d == nil {
		return nil, errors.New("tunnel is not configured")
	}
	for _, region := range d.RegionNames {
		regionTarget := WorkspaceTarget(workspace, port)
		if d.RegionTunnel != nil && d.RegionTunnel.Connected(region) {
			if conn, err := d.RegionTunnel.Dial(ctx, region, regionTarget); err == nil {
				return conn, nil
			}
		}
		if conn, err := d.dialPeer(ctx, KindRegion, region, regionTarget); err == nil {
			return conn, nil
		}
	}
	return nil, fmt.Errorf("workspace %q is not reachable in any region", workspace)
}

// PeerRegionDialer lets the hub's region client use the replica that owns the
// region tunnel, so any portal replica can serve region API requests.
type PeerRegionDialer struct {
	Local    *RegionBroker
	Presence Presence
	Address  string
	Key      []byte
}

func (d *PeerRegionDialer) Connected(region string) bool {
	if d == nil {
		return false
	}
	if d.Local != nil && d.Local.Connected(region) {
		return true
	}
	if d.Presence == nil || d.Address == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	address, ok, err := d.Presence.Lookup(ctx, KindRegion, region)
	return err == nil && ok && address != "" && address != d.Address
}

func (d *PeerRegionDialer) Dial(ctx context.Context, region, target string) (net.Conn, error) {
	if d == nil {
		return nil, errors.New("region tunnel is not configured")
	}
	if d.Local != nil && d.Local.Connected(region) {
		return d.Local.Dial(ctx, region, target)
	}
	if d.Presence == nil || d.Address == "" {
		return nil, fmt.Errorf("region %q tunnel is not connected", region)
	}
	address, ok, err := d.Presence.Lookup(ctx, KindRegion, region)
	if err != nil || !ok || address == "" || address == d.Address {
		return nil, fmt.Errorf("region %q tunnel is not connected", region)
	}
	return DialPeer(ctx, d.Key, address, KindRegion, region, target)
}

func (d *PeerDialer) dialPeer(ctx context.Context, kind, name, target string) (net.Conn, error) {
	if d.Presence == nil || d.Address == "" || len(d.Key) == 0 {
		return nil, errors.New("peer dialing is not configured")
	}
	address, ok, err := d.Presence.Lookup(ctx, kind, name)
	if err != nil || !ok || address == "" || address == d.Address {
		return nil, errors.New("peer tunnel not found")
	}
	return DialPeer(ctx, d.Key, address, kind, name, target)
}

// DialPeer opens a raw stream through a peer portal's internal dial endpoint.
func DialPeer(ctx context.Context, key []byte, address, kind, name, target string) (net.Conn, error) {
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	now := strconv.FormatInt(time.Now().Unix(), 10)
	encodedName := base64.RawURLEncoding.EncodeToString([]byte(name))
	request := fmt.Sprintf("POST /api/internal/dial HTTP/1.1\r\nHost: %s\r\nX-Poddy-Dial-Kind: %s\r\nX-Poddy-Dial-Name: %s\r\nX-Poddy-Dial-Target: %s\r\nX-Poddy-Dial-Time: %s\r\nX-Poddy-Dial-Auth: %s\r\nContent-Length: 0\r\nConnection: close\r\n\r\n",
		address, kind, encodedName, target, now, SignDial(key, now, kind, name, target))
	if _, err := conn.Write([]byte(request)); err != nil {
		_ = conn.Close()
		return nil, err
	}
	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, nil)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		_ = conn.Close()
		return nil, fmt.Errorf("peer dial: HTTP %d", resp.StatusCode)
	}
	_ = conn.SetDeadline(time.Time{})
	return &bufferedConn{Conn: conn, reader: reader}, nil
}

func SignDial(key []byte, timestamp, kind, name, target string) string {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(strings.Join([]string{timestamp, kind, name, target}, "\n")))
	return hex.EncodeToString(mac.Sum(nil))
}

func VerifyDial(key []byte, timestamp, kind, name, target, signature string) bool {
	if len(key) == 0 || signature == "" {
		return false
	}
	unix, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return false
	}
	if delta := time.Since(time.Unix(unix, 0)); delta > time.Minute || delta < -time.Minute {
		return false
	}
	got, err := hex.DecodeString(signature)
	if err != nil {
		return false
	}
	want, err := hex.DecodeString(SignDial(key, timestamp, kind, name, target))
	if err != nil {
		return false
	}
	return hmac.Equal(got, want)
}

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) {
	return c.reader.Read(p)
}

// ReportPresence renews leases for every live tunnel owned by this replica.
func ReportPresence(ctx context.Context, presence Presence, broker *Broker, regions *RegionBroker, address string) {
	if presence == nil || address == "" {
		return
	}
	claimed := map[string]bool{}
	report := func() {
		active := map[string]bool{}
		if broker != nil {
			for _, workspace := range broker.Workspaces() {
				active[KindWorkspace+"\x00"+workspace] = true
			}
		}
		if regions != nil {
			for _, region := range regions.Regions() {
				active[KindRegion+"\x00"+region] = true
			}
		}
		for key := range active {
			kind, name, _ := strings.Cut(key, "\x00")
			if err := presence.Claim(ctx, kind, name, address); err != nil {
				log.Printf("tunnel presence: claim %s %s: %v", kind, name, err)
			}
		}
		for key := range claimed {
			if active[key] {
				continue
			}
			kind, name, _ := strings.Cut(key, "\x00")
			_ = presence.Release(ctx, kind, name)
		}
		claimed = active
	}

	report()
	ticker := time.NewTicker(presenceInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			for key := range claimed {
				kind, name, _ := strings.Cut(key, "\x00")
				_ = presence.Release(context.Background(), kind, name)
			}
			return
		case <-ticker.C:
			report()
		}
	}
}
