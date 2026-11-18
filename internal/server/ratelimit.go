package server

import (
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	cliAuthorizeRatePerMinute = 20
	cliAuthorizeBurst         = 10
	cliTokenRatePerMinute     = 30
	cliTokenBurst             = 15
)

// rateLimiter is a per-key token bucket. Keys are client addresses for the
// unauthenticated CLI login endpoints.
type rateLimiter struct {
	mu        sync.Mutex
	rate      float64
	burst     float64
	buckets   map[string]*rateBucket
	lastSweep time.Time
}

type rateBucket struct {
	tokens float64
	last   time.Time
}

func newRateLimiter(perMinute, burst float64) *rateLimiter {
	return &rateLimiter{
		rate:    perMinute / 60,
		burst:   burst,
		buckets: map[string]*rateBucket{},
	}
}

func (l *rateLimiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	if now.Sub(l.lastSweep) > 5*time.Minute {
		for bucketKey, bucket := range l.buckets {
			if now.Sub(bucket.last) > 10*time.Minute {
				delete(l.buckets, bucketKey)
			}
		}
		l.lastSweep = now
	}

	bucket := l.buckets[key]
	if bucket == nil {
		bucket = &rateBucket{tokens: l.burst, last: now}
		l.buckets[key] = bucket
	}
	bucket.tokens = math.Min(l.burst, bucket.tokens+now.Sub(bucket.last).Seconds()*l.rate)
	bucket.last = now
	if bucket.tokens < 1 {
		return false
	}
	bucket.tokens--
	return true
}

func (s *Server) cliRateLimited(w http.ResponseWriter, r *http.Request, tokenEndpoint bool) bool {
	s.cliLimitMu.Lock()
	limiter := s.cliAuthorizeLimiter
	perMinute, burst := cliAuthorizeRatePerMinute, cliAuthorizeBurst
	if tokenEndpoint {
		limiter = s.cliTokenLimiter
		perMinute, burst = cliTokenRatePerMinute, cliTokenBurst
	}
	if limiter == nil {
		limiter = newRateLimiter(float64(perMinute), float64(burst))
		if tokenEndpoint {
			s.cliTokenLimiter = limiter
		} else {
			s.cliAuthorizeLimiter = limiter
		}
	}
	s.cliLimitMu.Unlock()

	if limiter.allow(clientRateKey(r), time.Now()) {
		return false
	}
	retry := int(math.Ceil(60 / float64(perMinute)))
	if retry < 1 {
		retry = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(retry))
	http.Error(w, "too many CLI login attempts", http.StatusTooManyRequests)
	return true
}

func clientRateKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() && !ip.IsPrivate() {
		return host
	}
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		return strings.TrimSpace(strings.Split(forwarded, ",")[0])
	}
	if real := r.Header.Get("X-Real-IP"); real != "" {
		return strings.TrimSpace(real)
	}
	return host
}
