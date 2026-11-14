package secure

import "testing"

func TestEncryptDecrypt(t *testing.T) {
	key := Key("test-key")
	plaintext := []byte(`{"accessToken":"secret"}`)

	encrypted, err := Encrypt(key, plaintext)
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	if string(encrypted) == string(plaintext) {
		t.Fatal("Encrypt() returned plaintext")
	}
	got, err := Decrypt(key, encrypted)
	if err != nil {
		t.Fatalf("Decrypt() error = %v", err)
	}
	if string(got) != string(plaintext) {
		t.Fatalf("Decrypt() = %q, want %q", got, plaintext)
	}
}

func TestEncryptDecryptRejectsWrongKey(t *testing.T) {
	encrypted, err := Encrypt(Key("key-a"), []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decrypt(Key("key-b"), encrypted); err == nil {
		t.Fatal("Decrypt() with wrong key succeeded")
	}
	if _, err := Encrypt(nil, []byte("secret")); err == nil {
		t.Fatal("Encrypt() without a key succeeded")
	}
}

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
