package tunnel

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDialHandlerRejectsInvalidSignature(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/internal/dial", nil)
	req.Header.Set("X-Poddy-Dial-Kind", KindRegion)
	req.Header.Set("X-Poddy-Dial-Name", "eu")
	req.Header.Set("X-Poddy-Dial-Target", RegionHTTPTarget)
	req.Header.Set("X-Poddy-Dial-Time", "now")
	req.Header.Set("X-Poddy-Dial-Auth", "bad")
	rec := httptest.NewRecorder()
	ServeDial(rec, req, []byte("key"), nil, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d", rec.Code)
	}
}
