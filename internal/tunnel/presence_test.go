package tunnel

import (
	"strconv"
	"testing"
	"time"
)

func TestDialSignature(t *testing.T) {
	key := []byte("secret")
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	sig := SignDial(key, ts, KindWorkspace, "demo", "127.0.0.1:22")
	if !VerifyDial(key, ts, KindWorkspace, "demo", "127.0.0.1:22", sig) {
		t.Fatal("valid signature rejected")
	}
	if VerifyDial(key, ts, KindWorkspace, "other", "127.0.0.1:22", sig) {
		t.Fatal("signature accepted for another workspace")
	}
	if VerifyDial([]byte("other"), ts, KindWorkspace, "demo", "127.0.0.1:22", sig) {
		t.Fatal("signature accepted with another key")
	}
}

func TestDialSignatureRejectsOldRequests(t *testing.T) {
	key := []byte("secret")
	text := strconv.FormatInt(time.Now().Add(-2*time.Minute).Unix(), 10)
	sig := SignDial(key, text, KindWorkspace, "demo", "target")
	if VerifyDial(key, text, KindWorkspace, "demo", "target", sig) {
		t.Fatal("stale signature accepted")
	}
}
