package main

import (
	"net"
	"testing"
)

// inner IPv4 header (20 bytes) + first 8 bytes of the original L4 header
func innerPkt(proto byte, l4 []byte) []byte {
	ip := make([]byte, 20)
	ip[0] = 0x45
	ip[9] = proto
	copy(ip[12:16], net.IPv4(192, 168, 1, 10).To4())
	copy(ip[16:20], net.IPv4(1, 2, 3, 4).To4())
	return append(ip, l4...)
}

func timeExceeded(inner []byte) []byte {
	return append([]byte{11, 0, 0, 0, 0, 0, 0, 0}, inner...)
}

func TestParseTimeExceededUDP(t *testing.T) {
	udp := []byte{0x82, 0x9a, 0x69, 0x87, 0, 40, 0, 0} // src 33434, dst 27015
	k, ev, ok := parseICMP(timeExceeded(innerPkt(17, udp)))
	if !ok || k != (icmpKey{17, 33434, 0}) || ev.Type != 11 {
		t.Fatalf("udp: %v %v %v", k, ev, ok)
	}
}

func TestParseTimeExceededTCP(t *testing.T) {
	tcp := []byte{0xd4, 0x31, 0x63, 0xdd, 1, 2, 3, 4} // src 54321, dst 25565
	k, _, ok := parseICMP(timeExceeded(innerPkt(6, tcp)))
	if !ok || k != (icmpKey{6, 54321, 0}) {
		t.Fatalf("tcp: %v %v", k, ok)
	}
}

func TestParseTimeExceededICMPAndReply(t *testing.T) {
	echo := buildEcho(0x1234, 7)[:8]
	k, _, ok := parseICMP(timeExceeded(innerPkt(1, echo)))
	if !ok || k != (icmpKey{1, 0x1234, 7}) {
		t.Fatalf("icmp: %v %v", k, ok)
	}
	reply := buildEcho(0x1234, 7)
	reply[0] = 0
	k, ev, ok := parseICMP(reply)
	if !ok || k != (icmpKey{1, 0x1234, 7}) || ev.Type != 0 {
		t.Fatalf("reply: %v %v %v", k, ev, ok)
	}
}

func TestChecksum(t *testing.T) {
	p := buildEcho(1, 1)
	if checksum(p) != 0 {
		t.Fatal("echo checksum must verify to 0")
	}
}

func TestClassify(t *testing.T) {
	d := net.IPv4(1, 2, 3, 4)
	if k, r, f := classify(icmpEvent{Type: 11}, d); k != "TTL exceeded" || r || f {
		t.Fatal("ttl")
	}
	if _, r, _ := classify(icmpEvent{Type: 3, Code: 3, From: d}, d); !r {
		t.Fatal("port unreachable should mean reached")
	}
	if _, r, f := classify(icmpEvent{Type: 3, Code: 13, From: net.IPv4(9, 9, 9, 9)}, d); r || !f {
		t.Fatal("admin prohibited mid-path is final but not reached")
	}
}
