package netmon

import (
	"encoding/binary"
	"testing"
)

func clientHello(host string) []byte {
	ext := make([]byte, 0, len(host)+9)
	ext = append(ext, 0, 0)
	ext = append(ext, byte((len(host)+5)>>8), byte(len(host)+5))
	ext = append(ext, byte((len(host)+3)>>8), byte(len(host)+3))
	ext = append(ext, 0)
	ext = append(ext, byte((len(host))>>8), byte(len(host)))
	ext = append(ext, host...)

	body := []byte{3, 3}
	body = append(body, make([]byte, 32)...)
	body = append(body, 0)
	body = append(body, 0, 2, 0x13, 0x01)
	body = append(body, 1, 0)
	body = append(body, byte((len(ext))>>8), byte(len(ext)))
	body = append(body, ext...)

	handshake := []byte{1, byte(len(body) >> 16), byte(len(body) >> 8), byte(len(body))}
	handshake = append(handshake, body...)
	record := []byte{22, 3, 1, 0, 0}
	binary.BigEndian.PutUint16(record[3:5], uint16(len(handshake)))
	return append(record, handshake...)
}

func TestParseServerName(t *testing.T) {
	host, ok := ParseServerName(clientHello("bucket.s3.us-east-1.amazonaws.com"))
	if !ok || host != "bucket.s3.us-east-1.amazonaws.com" {
		t.Fatalf("host = %q, ok = %v", host, ok)
	}
	if _, ok := ParseServerName([]byte("not tls")); ok {
		t.Fatal("expected invalid payload to fail")
	}
}

func TestSNICache(t *testing.T) {
	cache := NewSNICache(10)
	cache.Observe("203.0.113.10", 443, clientHello("bucket.s3.us-east-1.amazonaws.com"))
	host, ok := cache.Lookup("203.0.113.10", 443)
	if !ok || host != "bucket.s3.us-east-1.amazonaws.com" {
		t.Fatalf("lookup = %q, %v", host, ok)
	}
}
