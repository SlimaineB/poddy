package k8s

import "testing"

func TestParseProcNetTCP(t *testing.T) {
	data := []byte(`  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 0100007F:0BB8 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 1
   1: 00000000:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 2
   2: 0100007F:0016 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 3
   3: 0100007F:1388 00000000:0000 01 00000000:00000000 00:00000000 00000000  1000        0 4
   4: 00000000000000000000000000000000:1F40 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 5
`)

	ports := parseProcNetTCP(data)
	if len(ports) != 3 {
		t.Fatalf("ports = %+v", ports)
	}
	if ports[0].Port != 3000 || ports[0].Address != "127.0.0.1" {
		t.Fatalf("first port = %+v", ports[0])
	}
	if ports[1].Port != 8000 || ports[1].Address != "::" {
		t.Fatalf("second port = %+v", ports[1])
	}
	if ports[2].Port != 8080 || ports[2].Address != "0.0.0.0" {
		t.Fatalf("third port = %+v", ports[2])
	}
}

func TestParseProcNetTCPIPv6Loopback(t *testing.T) {
	data := []byte(`
   0: 00000000000000000000000001000000:0BB8 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 1
`)
	ports := parseProcNetTCP(data)
	if len(ports) != 1 || ports[0].Address != "::1" || ports[0].Port != 3000 {
		t.Fatalf("ports = %+v", ports)
	}
}

func TestParseProcNetTCPPrefersWildcard(t *testing.T) {
	data := []byte(`
   0: 0100007F:0BB8 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 1
   1: 00000000:0BB8 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 2
`)
	ports := parseProcNetTCP(data)
	if len(ports) != 1 || ports[0].Address != "0.0.0.0" {
		t.Fatalf("ports = %+v", ports)
	}
}
