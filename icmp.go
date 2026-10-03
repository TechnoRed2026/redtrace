package main

import (
	"errors"
	"net"
	"sync"
	"syscall"
	"time"
)

type icmpKey struct {
	proto byte // 1 = ICMP echo, 6 = TCP, 17 = UDP
	a, b  uint16
}

type icmpEvent struct {
	From net.IP
	Type byte
	Code byte
	At   time.Time
}

// listener reads every ICMP packet arriving on the raw socket and dispatches
// the ones that belong to one of our in-flight probes.
type listener struct {
	conn *net.IPConn
	mu   sync.Mutex
	w    map[icmpKey]chan icmpEvent
}

func newListener(c *net.IPConn) *listener {
	l := &listener{conn: c, w: map[icmpKey]chan icmpEvent{}}
	go l.loop()
	return l
}

func (l *listener) register(k icmpKey) chan icmpEvent {
	ch := make(chan icmpEvent, 4)
	l.mu.Lock()
	l.w[k] = ch
	l.mu.Unlock()
	return ch
}

func (l *listener) unregister(k icmpKey) {
	l.mu.Lock()
	delete(l.w, k)
	l.mu.Unlock()
}

func (l *listener) loop() {
	buf := make([]byte, 2048)
	for {
		n, addr, err := l.conn.ReadFrom(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			time.Sleep(2 * time.Millisecond)
			continue
		}
		now := time.Now()
		ia, ok := addr.(*net.IPAddr)
		if !ok {
			continue
		}
		k, ev, ok := parseICMP(buf[:n])
		if !ok {
			continue
		}
		ev.From = ia.IP
		ev.At = now
		l.mu.Lock()
		ch := l.w[k]
		l.mu.Unlock()
		if ch != nil {
			select {
			case ch <- ev:
			default:
			}
		}
	}
}

func be16(b []byte) uint16 { return uint16(b[0])<<8 | uint16(b[1]) }

// parseICMP understands echo replies and the error messages
// (time exceeded / unreachable / parameter problem) that embed our original packet.
func parseICMP(b []byte) (icmpKey, icmpEvent, bool) {
	var k icmpKey
	if len(b) < 8 {
		return k, icmpEvent{}, false
	}
	t, c := b[0], b[1]
	switch t {
	case 0:
		return icmpKey{1, be16(b[4:6]), be16(b[6:8])}, icmpEvent{Type: t, Code: c}, true
	case 3, 11, 12:
		in := b[8:]
		if len(in) < 28 || in[0]>>4 != 4 {
			return k, icmpEvent{}, false
		}
		ihl := int(in[0]&0x0f) * 4
		if ihl < 20 || len(in) < ihl+8 {
			return k, icmpEvent{}, false
		}
		proto := in[9]
		pl := in[ihl:]
		switch proto {
		case 1:
			if pl[0] != 8 {
				return k, icmpEvent{}, false
			}
			return icmpKey{1, be16(pl[4:6]), be16(pl[6:8])}, icmpEvent{Type: t, Code: c}, true
		case 6, 17:
			return icmpKey{proto, be16(pl[0:2]), 0}, icmpEvent{Type: t, Code: c}, true
		}
	}
	return k, icmpEvent{}, false
}

func checksum(b []byte) uint16 {
	var s uint32
	for i := 0; i+1 < len(b); i += 2 {
		s += uint32(b[i])<<8 | uint32(b[i+1])
	}
	if len(b)%2 == 1 {
		s += uint32(b[len(b)-1]) << 8
	}
	for s>>16 != 0 {
		s = s&0xffff + s>>16
	}
	return ^uint16(s)
}

func buildEcho(id, seq uint16) []byte {
	p := make([]byte, 8+32)
	p[0] = 8
	p[4], p[5] = byte(id>>8), byte(id)
	p[6], p[7] = byte(seq>>8), byte(seq)
	for i := 8; i < len(p); i++ {
		p[i] = byte(i)
	}
	cs := checksum(p)
	p[2], p[3] = byte(cs>>8), byte(cs)
	return p
}

func setTTLConn(c syscall.Conn, ttl int) error {
	rc, err := c.SyscallConn()
	if err != nil {
		return err
	}
	var serr error
	if err := rc.Control(func(fd uintptr) { serr = setTTLFd(fd, ttl) }); err != nil {
		return err
	}
	return serr
}
