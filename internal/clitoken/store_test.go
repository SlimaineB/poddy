package clitoken

import (
	"context"
	"testing"
	"time"
)

func TestMemoryRevocationAndCodeConsumption(t *testing.T) {
	ctx := context.Background()
	store := NewMemory()
	expires := time.Now().Add(time.Hour)

	if revoked, err := store.Revoked(ctx, "token"); err != nil || revoked {
		t.Fatalf("initial revoked = %v, %v", revoked, err)
	}
	if err := store.Revoke(ctx, "token", expires); err != nil {
		t.Fatal(err)
	}
	if revoked, err := store.Revoked(ctx, "token"); err != nil || !revoked {
		t.Fatalf("revoked = %v, %v", revoked, err)
	}

	ok, err := store.ConsumeCode(ctx, "code", expires)
	if err != nil || !ok {
		t.Fatalf("first consume = %v, %v", ok, err)
	}
	ok, err = store.ConsumeCode(ctx, "code", expires)
	if err != nil || ok {
		t.Fatalf("second consume = %v, %v", ok, err)
	}
}

func TestMemoryPurgesExpiredEntries(t *testing.T) {
	ctx := context.Background()
	store := NewMemory()
	expires := time.Now().Add(-time.Minute)
	if err := store.Revoke(ctx, "token", expires); err != nil {
		t.Fatal(err)
	}
	if ok, err := store.ConsumeCode(ctx, "code", expires); err != nil || !ok {
		t.Fatalf("consume = %v, %v", ok, err)
	}
	if revoked, err := store.Revoked(ctx, "token"); err != nil || revoked {
		t.Fatalf("expired revoked = %v, %v", revoked, err)
	}
	if ok, err := store.ConsumeCode(ctx, "code", time.Now().Add(time.Hour)); err != nil || !ok {
		t.Fatalf("consume after expiry = %v, %v", ok, err)
	}
}
