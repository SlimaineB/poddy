package secure

import "testing"

func TestSignVerify(t *testing.T) {
	key := []byte("test-key")
	payload := []byte(`{"sub":"alice"}`)

	value := Sign(key, payload)
	got, err := Verify(key, value)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("Verify() = %q, want %q", got, payload)
	}
}

func TestVerifyRejectsTampering(t *testing.T) {
	key := []byte("test-key")
	value := Sign(key, []byte("payload"))

	if _, err := Verify([]byte("other-key"), value); err == nil {
		t.Fatal("Verify() with wrong key succeeded")
	}
	if _, err := Verify(key, value+"x"); err == nil {
		t.Fatal("Verify() with tampered value succeeded")
	}
	if _, err := Verify(key, "not-a-token"); err == nil {
		t.Fatal("Verify() with malformed value succeeded")
	}
}

func TestRandomString(t *testing.T) {
	a, err := RandomString(32)
	if err != nil {
		t.Fatalf("RandomString() error = %v", err)
	}
	b, err := RandomString(32)
	if err != nil {
		t.Fatalf("RandomString() error = %v", err)
	}
	if a == "" || a == b {
		t.Fatalf("RandomString() = %q, %q; want distinct non-empty values", a, b)
	}
}
