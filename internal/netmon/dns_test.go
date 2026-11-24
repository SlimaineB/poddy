package netmon

import (
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

func dnsResponse(t *testing.T, host string, ip [4]byte) []byte {
	t.Helper()
	name, err := dnsmessage.NewName(host + ".")
	if err != nil {
		t.Fatal(err)
	}
	builder := dnsmessage.NewBuilder(nil, dnsmessage.Header{
		Response: true,
		RCode:    dnsmessage.RCodeSuccess,
	})
	builder.EnableCompression()
	if err := builder.StartQuestions(); err != nil {
		t.Fatal(err)
	}
	if err := builder.Question(dnsmessage.Question{Name: name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET}); err != nil {
		t.Fatal(err)
	}
	if err := builder.StartAnswers(); err != nil {
		t.Fatal(err)
	}
	if err := builder.AResource(dnsmessage.ResourceHeader{
		Name:  name,
		Type:  dnsmessage.TypeA,
		Class: dnsmessage.ClassINET,
		TTL:   60,
	}, dnsmessage.AResource{A: ip}); err != nil {
		t.Fatal(err)
	}
	msg, err := builder.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return msg
}

func TestParseDNS(t *testing.T) {
	payload := dnsResponse(t, "bucket.s3.us-east-1.amazonaws.com", [4]byte{203, 0, 113, 10})
	host, ips, ttl, response, err := ParseDNS(payload)
	if err != nil {
		t.Fatal(err)
	}
	if !response {
		t.Fatal("expected response")
	}
	if host != "bucket.s3.us-east-1.amazonaws.com" {
		t.Fatalf("host = %q", host)
	}
	if len(ips) != 1 || ips[0] != "203.0.113.10" {
		t.Fatalf("ips = %#v", ips)
	}
	if ttl != 60*time.Second {
		t.Fatalf("ttl = %s", ttl)
	}
}

func TestDNSCache(t *testing.T) {
	cache := NewDNSCache(10)
	payload := dnsResponse(t, "bucket.s3.us-east-1.amazonaws.com", [4]byte{203, 0, 113, 10})
	cache.Observe(payload)
	host, ok := cache.Lookup("203.0.113.10")
	if !ok || host != "bucket.s3.us-east-1.amazonaws.com" {
		t.Fatalf("lookup = %q, %v", host, ok)
	}
	if cache.Len() != 1 {
		t.Fatalf("len = %d", cache.Len())
	}
}
