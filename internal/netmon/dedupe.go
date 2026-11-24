package netmon

import (
	"fmt"
	"hash/fnv"
	"sync"
	"time"
)

type deduper struct {
	mu    sync.Mutex
	seen  map[string]time.Time
	ttl   time.Duration
	max   int
	now   func() time.Time
	last  time.Time
	count int
}

func newDeduper(ttl time.Duration, max int) *deduper {
	if ttl <= 0 {
		ttl = 2 * time.Second
	}
	if max <= 0 {
		max = 65536
	}
	return &deduper{
		seen: make(map[string]time.Time),
		ttl:  ttl,
		max:  max,
		now:  time.Now,
	}
}

func (d *deduper) Seen(packet Packet) bool {
	key := dedupeKey(packet)
	now := d.now()
	d.mu.Lock()
	defer d.mu.Unlock()
	if expires, ok := d.seen[key]; ok && now.Before(expires) {
		return true
	}
	if len(d.seen) >= d.max {
		d.pruneLocked(now)
		if len(d.seen) >= d.max {
			d.seen = make(map[string]time.Time)
		}
	}
	d.seen[key] = now.Add(d.ttl)
	if now.Sub(d.last) > time.Second {
		d.last = now
		d.count = len(d.seen)
	}
	return false
}

func (d *deduper) pruneLocked(now time.Time) {
	for key, expires := range d.seen {
		if now.After(expires) {
			delete(d.seen, key)
		}
	}
}

func dedupeKey(packet Packet) string {
	h := fnv.New64a()
	_, _ = h.Write(packet.Payload)
	return fmt.Sprintf("%s|%s|%d|%d|%s|%d|%d|%x",
		packet.SrcIP,
		packet.DstIP,
		packet.SrcPort,
		packet.DstPort,
		packet.Protocol,
		packet.IPID,
		packet.TCPSeq,
		h.Sum64(),
	)
}
