package netmon

import (
	"encoding/binary"
	"net"
	"time"
)

const (
	etherTypeIPv4 = 0x0800
	etherTypeIPv6 = 0x86dd
	etherTypeVLAN = 0x8100
	etherTypeQinQ = 0x88a8
)

func ParsePacket(frame []byte, timestamp time.Time) (Packet, bool) {
	if len(frame) < 14 {
		return Packet{}, false
	}
	etherType := binary.BigEndian.Uint16(frame[12:14])
	offset := 14
	for etherType == etherTypeVLAN || etherType == etherTypeQinQ {
		if len(frame) < offset+4 {
			return Packet{}, false
		}
		etherType = binary.BigEndian.Uint16(frame[offset+2 : offset+4])
		offset += 4
	}
	switch etherType {
	case etherTypeIPv4:
		return parseIPv4(frame[offset:], timestamp)
	case etherTypeIPv6:
		return parseIPv6(frame[offset:], timestamp)
	default:
		return Packet{}, false
	}
}

func parseIPv4(data []byte, timestamp time.Time) (Packet, bool) {
	if len(data) < 20 {
		return Packet{}, false
	}
	ihl := int(data[0]&0x0f) * 4
	if ihl < 20 || len(data) < ihl {
		return Packet{}, false
	}
	totalLen := int(binary.BigEndian.Uint16(data[2:4]))
	if totalLen < ihl || totalLen > len(data) {
		totalLen = len(data)
	}
	flagsFrag := binary.BigEndian.Uint16(data[6:8])
	if flagsFrag&0x1fff != 0 {
		return Packet{}, false
	}
	protocol := data[9]
	src := net.IP(data[12:16]).String()
	dst := net.IP(data[16:20]).String()
	payload := data[ihl:totalLen]
	packet := Packet{
		SrcIP:     src,
		DstIP:     dst,
		IPID:      binary.BigEndian.Uint16(data[4:6]),
		Length:    totalLen,
		Timestamp: timestamp,
	}
	switch protocol {
	case 6:
		return parseTCP(packet, payload)
	case 17:
		return parseUDP(packet, payload)
	default:
		return Packet{}, false
	}
}

func parseIPv6(data []byte, timestamp time.Time) (Packet, bool) {
	if len(data) < 40 {
		return Packet{}, false
	}
	payloadLen := int(binary.BigEndian.Uint16(data[4:6]))
	if 40+payloadLen > len(data) {
		payloadLen = len(data) - 40
	}
	next := data[6]
	src := net.IP(data[8:24]).String()
	dst := net.IP(data[24:40]).String()
	payload := data[40 : 40+payloadLen]
	for {
		switch next {
		case 0, 43, 60:
			if len(payload) < 8 {
				return Packet{}, false
			}
			next = payload[0]
			extLen := (int(payload[1]) + 1) * 8
			if len(payload) < extLen {
				return Packet{}, false
			}
			payload = payload[extLen:]
		case 44:
			if len(payload) < 8 {
				return Packet{}, false
			}
			next = payload[0]
			frag := binary.BigEndian.Uint16(payload[2:4])
			if frag&0xfff8 != 0 {
				return Packet{}, false
			}
			payload = payload[8:]
		default:
			goto parsed
		}
	}
parsed:
	packet := Packet{
		SrcIP:     src,
		DstIP:     dst,
		Length:    40 + payloadLen,
		Timestamp: timestamp,
	}
	switch next {
	case 6:
		return parseTCP(packet, payload)
	case 17:
		return parseUDP(packet, payload)
	default:
		return Packet{}, false
	}
}

func parseTCP(packet Packet, data []byte) (Packet, bool) {
	if len(data) < 20 {
		return Packet{}, false
	}
	dataOffset := int(data[12]>>4) * 4
	if dataOffset < 20 || dataOffset > len(data) {
		return Packet{}, false
	}
	packet.SrcPort = binary.BigEndian.Uint16(data[0:2])
	packet.DstPort = binary.BigEndian.Uint16(data[2:4])
	packet.TCPSeq = binary.BigEndian.Uint32(data[4:8])
	packet.TCPFlags = data[13]
	packet.Protocol = "tcp"
	packet.Payload = data[dataOffset:]
	return packet, true
}

func parseUDP(packet Packet, data []byte) (Packet, bool) {
	if len(data) < 8 {
		return Packet{}, false
	}
	packet.SrcPort = binary.BigEndian.Uint16(data[0:2])
	packet.DstPort = binary.BigEndian.Uint16(data[2:4])
	packet.Protocol = "udp"
	packet.Payload = data[8:]
	return packet, true
}
