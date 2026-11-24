package netmon

import (
	"encoding/binary"
	"testing"
	"time"
)

func ipv4Frame(src, dst [4]byte, protocol byte, id uint16, payload []byte) []byte {
	ether := make([]byte, 14)
	binary.BigEndian.PutUint16(ether[12:14], etherTypeIPv4)
	ip := make([]byte, 20)
	ip[0] = 0x45
	total := 20 + len(payload)
	binary.BigEndian.PutUint16(ip[2:4], uint16(total))
	binary.BigEndian.PutUint16(ip[4:6], id)
	ip[8] = 64
	ip[9] = protocol
	copy(ip[12:16], src[:])
	copy(ip[16:20], dst[:])
	return append(append(ether, ip...), payload...)
}

func tcpSegment(srcPort, dstPort uint16, seq uint32, flags byte, payload []byte) []byte {
	header := make([]byte, 20)
	binary.BigEndian.PutUint16(header[0:2], srcPort)
	binary.BigEndian.PutUint16(header[2:4], dstPort)
	binary.BigEndian.PutUint32(header[4:8], seq)
	header[12] = 5 << 4
	header[13] = flags
	return append(header, payload...)
}

func udpSegment(srcPort, dstPort uint16, payload []byte) []byte {
	header := make([]byte, 8)
	binary.BigEndian.PutUint16(header[0:2], srcPort)
	binary.BigEndian.PutUint16(header[2:4], dstPort)
	binary.BigEndian.PutUint16(header[4:6], uint16(8+len(payload)))
	return append(header, payload...)
}

func TestParsePacketTCP(t *testing.T) {
	frame := ipv4Frame([4]byte{10, 244, 0, 2}, [4]byte{203, 0, 113, 10}, 6, 42,
		tcpSegment(12345, 443, 100, 0x02, []byte("hello")))
	packet, ok := ParsePacket(frame, time.Now())
	if !ok {
		t.Fatal("expected packet")
	}
	if packet.SrcIP != "10.244.0.2" || packet.DstIP != "203.0.113.10" {
		t.Fatalf("addresses = %s -> %s", packet.SrcIP, packet.DstIP)
	}
	if packet.SrcPort != 12345 || packet.DstPort != 443 || packet.Protocol != "tcp" {
		t.Fatalf("ports = %d -> %d, protocol = %s", packet.SrcPort, packet.DstPort, packet.Protocol)
	}
	if packet.TCPFlags != 0x02 || string(packet.Payload) != "hello" {
		t.Fatalf("flags = %x, payload = %q", packet.TCPFlags, packet.Payload)
	}
}

func TestParsePacketUDP(t *testing.T) {
	frame := ipv4Frame([4]byte{10, 244, 0, 2}, [4]byte{10, 43, 0, 10}, 17, 43,
		udpSegment(5353, 53, []byte("dns")))
	packet, ok := ParsePacket(frame, time.Now())
	if !ok {
		t.Fatal("expected packet")
	}
	if packet.Protocol != "udp" || packet.DstPort != 53 || string(packet.Payload) != "dns" {
		t.Fatalf("packet = %#v", packet)
	}
}
