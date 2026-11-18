package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRateLimiterBurstAndRefill(t *testing.T) {
	limiter := newRateLimiter(60, 2)
	now := time.Unix(0, 0)
	if !limiter.allow("client", now) || !limiter.allow("client", now) {
		t.Fatal("burst requests rejected")
	}
	if limiter.allow("client", now) {
		t.Fatal("request beyond burst accepted")
	}
	if !limiter.allow("client", now.Add(time.Second)) {
		t.Fatal("refilled request rejected")
	}
	if !limiter.allow("other", now) {
		t.Fatal("per-key limit leaked")
	}
}

func TestCLITokenRateLimited(t *testing.T) {
	s := newCLITestServer()
	request := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/cli/token", strings.NewReader("{}"))
		req.RemoteAddr = "203.0.113.9:1234"
		rec := httptest.NewRecorder()
		s.cliToken(rec, req)
		return rec
	}
	for i := 0; i < cliTokenBurst; i++ {
		rec := request()
		if rec.Code == http.StatusTooManyRequests {
			t.Fatalf("request %d rate limited too early", i+1)
		}
	}
	rec := request()
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if retry := rec.Header().Get("Retry-After"); retry != "2" {
		t.Fatalf("Retry-After = %q", retry)
	}
}

func TestClientRateKeyOnlyTrustsProxyHeadersFromPrivatePeers(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.9:1234"
	req.Header.Set("X-Forwarded-For", "198.51.100.7")
	if got := clientRateKey(req); got != "203.0.113.9" {
		t.Fatalf("public peer key = %q", got)
	}

	req.RemoteAddr = "10.0.0.5:1234"
	if got := clientRateKey(req); got != "198.51.100.7" {
		t.Fatalf("proxy peer key = %q", got)
	}
}
