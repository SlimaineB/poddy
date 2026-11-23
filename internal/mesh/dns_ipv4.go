package mesh

import (
	"context"
	"net"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"

	dns "golang.org/x/net/dns/dnsmessage"
	"tailscale.com/net/dnscache"
)

var (
	dnsForwardMu   sync.Mutex
	dnsForwardHost string
)

// preferIPv4Control installs an A-only DNS forwarder for the control host. The
// hostname is kept in the control URL so TLS SNI and certificate verification
// still use the name.
//
// The forwarder is installed unconditionally for hostname control URLs because
// the resolver may be temporarily unable to return an IPv4 address during
// startup. Deciding whether to drop AAAA is done per query instead, so an
// IPv6-only control host still works.
func preferIPv4Control(controlURL string) {
	u, err := url.Parse(strings.TrimSpace(controlURL))
	if err != nil || u.Hostname() == "" {
		return
	}
	host := normalizeDNSName(u.Hostname())
	if _, err := netip.ParseAddr(host); err == nil {
		return
	}
	installIPv4DNSForwarder(host)
}

func installIPv4DNSForwarder(controlHost string) {
	dnsForwardMu.Lock()
	defer dnsForwardMu.Unlock()
	if dnsForwardHost == controlHost {
		return
	}
	dnsForwardHost = controlHost
	dnscache.Get().Forward = &net.Resolver{
		PreferGo: true,
		Dial: func(context.Context, string, string) (net.Conn, error) {
			client, server := net.Pipe()
			go serveIPv4DNS(server, controlHost)
			return pipePacketConn{Conn: client}, nil
		},
	}
}

type pipePacketConn struct {
	net.Conn
}

func (c pipePacketConn) ReadFrom(p []byte) (int, net.Addr, error) {
	n, err := c.Read(p)
	return n, pipeAddr{}, err
}

func (c pipePacketConn) WriteTo(p []byte, _ net.Addr) (int, error) {
	return c.Write(p)
}

type pipeAddr struct{}

func (pipeAddr) Network() string { return "udp" }
func (pipeAddr) String() string  { return "poddy-dns" }

func serveIPv4DNS(conn net.Conn, controlHost string) {
	defer conn.Close()
	buf := make([]byte, 64<<10)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			return
		}
		response, err := handleDNSQuery(buf[:n], controlHost, net.DefaultResolver.LookupNetIP)
		if err != nil {
			return
		}
		if _, err := conn.Write(response); err != nil {
			return
		}
	}
}

type lookupIPFunc func(ctx context.Context, network, host string) ([]netip.Addr, error)

func handleDNSQuery(query []byte, controlHost string, lookup lookupIPFunc) ([]byte, error) {
	var parser dns.Parser
	header, err := parser.Start(query)
	if err != nil {
		return nil, err
	}
	question, err := parser.Question()
	if err != nil {
		return nil, err
	}
	name := normalizeDNSName(question.Name.String())

	builder := dns.NewBuilder(nil, dns.Header{
		ID:                 header.ID,
		Response:           true,
		OpCode:             header.OpCode,
		RecursionDesired:   header.RecursionDesired,
		RecursionAvailable: true,
	})
	builder.EnableCompression()
	if err := builder.StartQuestions(); err != nil {
		return nil, err
	}
	if err := builder.Question(question); err != nil {
		return nil, err
	}
	if err := builder.StartAnswers(); err != nil {
		return nil, err
	}

	lookupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	switch question.Type {
	case dns.TypeA:
		ips, _ := lookup(lookupCtx, "ip4", name)
		for _, ip := range ips {
			if !ip.Is4() {
				continue
			}
			resource := dns.ResourceHeader{Name: question.Name, Type: dns.TypeA, Class: dns.ClassINET, TTL: 300}
			if err := builder.AResource(resource, dns.AResource{A: ip.As4()}); err != nil {
				return nil, err
			}
		}
	case dns.TypeAAAA:
		// Drop a synthesized NAT64 address for the control host when the name
		// also has a real IPv4 address. The control client then only races the
		// IPv4 address. An IPv6-only control host keeps its normal AAAA answer.
		if name == controlHost {
			if ips, _ := lookup(lookupCtx, "ip4", name); len(ips) > 0 {
				break
			}
		}
		ips, _ := lookup(lookupCtx, "ip6", name)
		for _, ip := range ips {
			if !ip.Is6() || ip.Is4In6() {
				continue
			}
			resource := dns.ResourceHeader{Name: question.Name, Type: dns.TypeAAAA, Class: dns.ClassINET, TTL: 300}
			if err := builder.AAAAResource(resource, dns.AAAAResource{AAAA: ip.As16()}); err != nil {
				return nil, err
			}
		}
	}
	return builder.Finish()
}

func normalizeDNSName(name string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
}
