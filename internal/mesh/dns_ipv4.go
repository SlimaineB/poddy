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

// nat64Prefixes are the well-known and local-use NAT64 prefixes (RFC 6052 and
// RFC 8215). A DNS64 resolver synthesizes an AAAA in one of these ranges for
// an IPv4-only name. On some Windows/WSL setups the TCP connect to that
// synthesized address succeeds but the TLS handshake blackholes, which makes
// the control client hang until its deadline even though the IPv4 address
// works.
var nat64Prefixes = []netip.Prefix{
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
}

var (
	dnsForwardMu   sync.Mutex
	dnsForwardHost string
)

// preferIPv4Control installs an A-only DNS forwarder for the control host when
// the system resolver synthesizes a NAT64 AAAA for it. The hostname is kept in
// the control URL so TLS SNI and certificate verification still use the name.
func preferIPv4Control(ctx context.Context, controlURL string) {
	u, err := url.Parse(strings.TrimSpace(controlURL))
	if err != nil || u.Hostname() == "" {
		return
	}
	host := normalizeDNSName(u.Hostname())
	if _, err := netip.ParseAddr(host); err == nil {
		return
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupNetIP(lookupCtx, "ip", host)
	if err != nil {
		return
	}
	have4 := false
	haveNAT64 := false
	for _, ip := range ips {
		if ip.Unmap().Is4() {
			have4 = true
		}
		if ip.Is6() && !ip.Is4In6() && isNAT64(ip) {
			haveNAT64 = true
		}
	}
	if !have4 || !haveNAT64 {
		return
	}
	installIPv4DNSForwarder(host)
}

func isNAT64(ip netip.Addr) bool {
	for _, prefix := range nat64Prefixes {
		if prefix.Contains(ip) {
			return true
		}
	}
	return false
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
		// Drop the synthesized NAT64 address for the control host so the
		// control client only races the real IPv4 address. Other names keep
		// their normal IPv6 answers.
		if name != controlHost {
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
	}
	return builder.Finish()
}

func normalizeDNSName(name string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
}
