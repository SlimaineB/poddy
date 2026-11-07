package region

import (
	"strings"
	"testing"
	"time"

	"poddy/internal/auth"
)

func TestIdentityRoundTrip(t *testing.T) {
	token, err := SignIdentity("secret", Identity{User: auth.User{Username: "alice"}, Admin: true, Region: "eu"})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := VerifyIdentity("secret", token)
	if err != nil {
		t.Fatal(err)
	}
	if claims.User.Username != "alice" || !claims.Admin || claims.Region != "eu" {
		t.Fatalf("claims = %+v", claims)
	}
}

func TestIdentityRejectsTampering(t *testing.T) {
	token, err := SignIdentity("secret", Identity{User: auth.User{Username: "alice"}})
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(token, ".")
	parts[0] = strings.Repeat("A", len(parts[0]))
	if _, err := VerifyIdentity("secret", strings.Join(parts, ".")); err == nil {
		t.Fatal("tampered identity accepted")
	}
	if _, err := VerifyIdentity("other", token); err == nil {
		t.Fatal("identity with wrong key accepted")
	}
}

func TestIdentityRejectsExpired(t *testing.T) {
	token, err := SignIdentity("secret", Identity{
		User:    auth.User{Username: "alice"},
		Expires: time.Now().Add(-time.Minute).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyIdentity("secret", token); err == nil {
		t.Fatal("expired identity accepted")
	}
}

func TestParseRegionCoordinates(t *testing.T) {
	registry, err := Parse([]byte("regions:\n- name: eu\n  url: http://example\n  key: secret\n  lat: 50.1\n  lon: 8.7\n"))
	if err != nil {
		t.Fatal(err)
	}
	cfg, ok := registry.Get("eu")
	if !ok {
		t.Fatal("region not found")
	}
	if cfg.Lat != 50.1 || cfg.Lon != 8.7 {
		t.Fatalf("coordinates = %v,%v", cfg.Lat, cfg.Lon)
	}

	if _, err := Parse([]byte("regions:\n- name: eu\n  url: http://example\n  key: secret\n  lat: 91\n")); err == nil {
		t.Fatal("invalid latitude accepted")
	}
	if _, err := Parse([]byte("regions:\n- name: eu\n  url: http://example\n  key: secret\n  lon: -181\n")); err == nil {
		t.Fatal("invalid longitude accepted")
	}
}
