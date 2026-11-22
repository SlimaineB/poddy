package mesh

import (
	"context"
	"net/netip"
	"testing"

	dns "golang.org/x/net/dns/dnsmessage"
)

func TestHandleDNSQueryFiltersNAT64ForControlHost(t *testing.T) {
	lookup := func(_ context.Context, network, host string) ([]netip.Addr, error) {
		switch network {
		case "ip4":
			return []netip.Addr{netip.MustParseAddr("172.29.74.141")}, nil
		case "ip6":
			return []netip.Addr{netip.MustParseAddr("64:ff9b::ac1d:4a8d")}, nil
		default:
			return nil, nil
		}
	}

	t.Run("A control host", func(t *testing.T) {
		answers := parseAnswers(t, query(t, "172-29-74-141.sslip.io", dns.TypeA), "172-29-74-141.sslip.io", lookup)
		if len(answers) != 1 {
			t.Fatalf("got %d answers, want 1", len(answers))
		}
		if answers[0].Header.Type != dns.TypeA {
			t.Fatalf("got type %v, want A", answers[0].Header.Type)
		}
	})

	t.Run("AAAA control host", func(t *testing.T) {
		answers := parseAnswers(t, query(t, "172-29-74-141.sslip.io", dns.TypeAAAA), "172-29-74-141.sslip.io", lookup)
		if len(answers) != 0 {
			t.Fatalf("got %d answers, want 0", len(answers))
		}
	})

	t.Run("AAAA other host", func(t *testing.T) {
		answers := parseAnswers(t, query(t, "example.com", dns.TypeAAAA), "172-29-74-141.sslip.io", lookup)
		if len(answers) != 1 {
			t.Fatalf("got %d answers, want 1", len(answers))
		}
		if answers[0].Header.Type != dns.TypeAAAA {
			t.Fatalf("got type %v, want AAAA", answers[0].Header.Type)
		}
	})
}

func query(t *testing.T, host string, typ dns.Type) []byte {
	t.Helper()
	name, err := dns.NewName(host + ".")
	if err != nil {
		t.Fatal(err)
	}
	builder := dns.NewBuilder(nil, dns.Header{ID: 42, RecursionDesired: true})
	if err := builder.StartQuestions(); err != nil {
		t.Fatal(err)
	}
	if err := builder.Question(dns.Question{Name: name, Type: typ, Class: dns.ClassINET}); err != nil {
		t.Fatal(err)
	}
	msg, err := builder.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return msg
}

func parseAnswers(t *testing.T, query []byte, controlHost string, lookup lookupIPFunc) []dns.Resource {
	t.Helper()
	response, err := handleDNSQuery(query, controlHost, lookup)
	if err != nil {
		t.Fatal(err)
	}
	var parser dns.Parser
	header, err := parser.Start(response)
	if err != nil {
		t.Fatal(err)
	}
	if !header.Response {
		t.Fatal("response flag not set")
	}
	if _, err := parser.Question(); err != nil {
		t.Fatal(err)
	}
	if err := parser.SkipAllQuestions(); err != nil {
		t.Fatal(err)
	}
	answers, err := parser.AllAnswers()
	if err != nil {
		t.Fatal(err)
	}
	return answers
}
