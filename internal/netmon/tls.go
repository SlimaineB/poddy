package netmon

import (
	"encoding/binary"
	"strings"
	"sync"
	"time"
)

const (
	tlsHandshakeTypeClientHello = 1
	tlsExtensionServerName      = 0
	tlsServerNameHost           = 0
	defaultSNITTL               = 10 * time.Minute
	defaultSNIMaxEntries        = 4096
)

type sniEntry struct {
	host    string
	expires time.Time
}

type SNICache struct {
	mu      sync.RWMutex
	entries map[string]sniEntry
	max     int
	now     func() time.Time
}

func NewSNICache(max int) *SNICache {
	if max <= 0 {
		max = defaultSNIMaxEntries
	}
	return &SNICache{
		entries: make(map[string]sniEntry),
		max:     max,
		now:     time.Now,
	}
}

func (c *SNICache) Observe(ip string, port uint16, payload []byte) {
	if c == nil || ip == "" || len(payload) == 0 {
		return
	}
	host, ok := ParseServerName(payload)
	if !ok || host == "" {
		return
	}
	key := ip + ":" + portLabel(port)
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= c.max {
		for k := range c.entries {
			delete(c.entries, k)
			break
		}
	}
	c.entries[key] = sniEntry{host: strings.ToLower(host), expires: c.now().Add(defaultSNITTL)}
}

func (c *SNICache) Lookup(ip string, port uint16) (string, bool) {
	if c == nil || ip == "" {
		return "", false
	}
	key := ip + ":" + portLabel(port)
	c.mu.RLock()
	entry, ok := c.entries[key]
	c.mu.RUnlock()
	if !ok || c.now().After(entry.expires) {
		return "", false
	}
	return entry.host, true
}

func ParseServerName(payload []byte) (string, bool) {
	if len(payload) < 6 || payload[0] != 22 {
		return "", false
	}
	recordLen := int(binary.BigEndian.Uint16(payload[3:5]))
	if recordLen == 0 {
		return "", false
	}
	end := 5 + recordLen
	if end > len(payload) {
		end = len(payload)
	}
	if end < 9 || payload[5] != tlsHandshakeTypeClientHello {
		return "", false
	}
	handshakeLen := int(payload[6])<<16 | int(payload[7])<<8 | int(payload[8])
	hsEnd := 9 + handshakeLen
	if hsEnd > end {
		hsEnd = end
	}
	pos := 9
	if pos+2+32 > hsEnd {
		return "", false
	}
	pos += 2 + 32
	if pos+1 > hsEnd {
		return "", false
	}
	sessionLen := int(payload[pos])
	pos++
	if pos+sessionLen+2 > hsEnd {
		return "", false
	}
	pos += sessionLen
	cipherLen := int(binary.BigEndian.Uint16(payload[pos : pos+2]))
	pos += 2
	if pos+cipherLen+1 > hsEnd {
		return "", false
	}
	pos += cipherLen
	compressionLen := int(payload[pos])
	pos++
	if pos+compressionLen+2 > hsEnd {
		return "", false
	}
	pos += compressionLen
	extensionsLen := int(binary.BigEndian.Uint16(payload[pos : pos+2]))
	pos += 2
	extensionsEnd := pos + extensionsLen
	if extensionsEnd > hsEnd {
		extensionsEnd = hsEnd
	}
	for pos+4 <= extensionsEnd {
		extType := binary.BigEndian.Uint16(payload[pos : pos+2])
		extLen := int(binary.BigEndian.Uint16(payload[pos+2 : pos+4]))
		pos += 4
		if pos+extLen > extensionsEnd {
			return "", false
		}
		if extType == tlsExtensionServerName {
			return parseServerNameExtension(payload[pos : pos+extLen])
		}
		pos += extLen
	}
	return "", false
}

func parseServerNameExtension(data []byte) (string, bool) {
	if len(data) < 2 {
		return "", false
	}
	listLen := int(binary.BigEndian.Uint16(data[:2]))
	if listLen > len(data)-2 {
		listLen = len(data) - 2
	}
	pos := 2
	end := pos + listLen
	for pos+3 <= end {
		nameType := data[pos]
		nameLen := int(binary.BigEndian.Uint16(data[pos+1 : pos+3]))
		pos += 3
		if pos+nameLen > end {
			return "", false
		}
		if nameType == tlsServerNameHost && nameLen > 0 {
			return string(data[pos : pos+nameLen]), true
		}
		pos += nameLen
	}
	return "", false
}
