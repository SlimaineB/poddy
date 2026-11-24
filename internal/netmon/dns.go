package netmon

import (
	"net"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

const (
	defaultDNSMaxEntries = 8192
	minDNSCacheTTL       = 5 * time.Second
	maxDNSCacheTTL       = time.Hour
)

type dnsEntry struct {
	host    string
	expires time.Time
}

type DNSCache struct {
	mu      sync.RWMutex
	entries map[string]dnsEntry
	max     int
	now     func() time.Time
}

func NewDNSCache(max int) *DNSCache {
	if max <= 0 {
		max = defaultDNSMaxEntries
	}
	return &DNSCache{
		entries: make(map[string]dnsEntry),
		max:     max,
		now:     time.Now,
	}
}

func (c *DNSCache) Observe(payload []byte) {
	if c == nil || len(payload) == 0 {
		return
	}
	host, ips, ttl, response, err := ParseDNS(payload)
	if err != nil || !response || host == "" || len(ips) == 0 {
		return
	}
	if ttl < minDNSCacheTTL {
		ttl = minDNSCacheTTL
	}
	if ttl > maxDNSCacheTTL {
		ttl = maxDNSCacheTTL
	}
	expires := c.now().Add(ttl)

	c.mu.Lock()
	defer c.mu.Unlock()
	c.pruneLocked()
	for _, ip := range ips {
		if len(c.entries) >= c.max {
			c.evictOneLocked()
		}
		c.entries[ip] = dnsEntry{host: host, expires: expires}
	}
}

func (c *DNSCache) Lookup(ip string) (string, bool) {
	if c == nil || ip == "" {
		return "", false
	}
	c.mu.RLock()
	entry, ok := c.entries[ip]
	c.mu.RUnlock()
	if !ok || c.now().After(entry.expires) {
		return "", false
	}
	return entry.host, true
}

func (c *DNSCache) Len() int {
	if c == nil {
		return 0
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}

func (c *DNSCache) pruneLocked() {
	now := c.now()
	for ip, entry := range c.entries {
		if now.After(entry.expires) {
			delete(c.entries, ip)
		}
	}
}

func (c *DNSCache) evictOneLocked() {
	for ip := range c.entries {
		delete(c.entries, ip)
		return
	}
}

func netIPString(raw []byte) string {
	return net.IP(raw).String()
}

func ParseDNS(payload []byte) (host string, ips []string, ttl time.Duration, response bool, err error) {
	var parser dnsmessage.Parser
	header, err := parser.Start(payload)
	if err != nil {
		return "", nil, 0, false, err
	}
	response = header.Response
	questions, err := parser.AllQuestions()
	if err != nil {
		return "", nil, 0, response, err
	}
	if len(questions) > 0 {
		host = strings.TrimSuffix(strings.ToLower(questions[0].Name.String()), ".")
	}
	answers, err := parser.AllAnswers()
	if err != nil {
		return "", nil, 0, response, err
	}
	minTTL := uint32(0)
	for _, answer := range answers {
		switch body := answer.Body.(type) {
		case *dnsmessage.AResource:
			ips = append(ips, netIPString(body.A[:]))
		case *dnsmessage.AAAAResource:
			ips = append(ips, netIPString(body.AAAA[:]))
		default:
			continue
		}
		if minTTL == 0 || answer.Header.TTL < minTTL {
			minTTL = answer.Header.TTL
		}
	}
	ttl = time.Duration(minTTL) * time.Second
	return host, ips, ttl, response, nil
}
