package main

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

type Proto int

const (
	ProtoICMP Proto = iota
	ProtoUDP
	ProtoTCP
)

func (p Proto) String() string {
	switch p {
	case ProtoUDP:
		return "UDP"
	case ProtoTCP:
		return "TCP"
	}
	return "ICMP"
}

func (p Proto) Next() Proto { return (p + 1) % 3 }
func (p Proto) Prev() Proto { return (p + 2) % 3 }

type Config struct {
	Target  string
	Port    int
	Proto   Proto
	MaxHops int
	Probes  int
	Timeout time.Duration
	Service *Service
}

func (c Config) PortLabel() string {
	if c.Proto == ProtoICMP {
		return "ICMP"
	}
	return fmt.Sprintf("%s:%d", c.Proto, c.Port)
}

func (c Config) Label() string {
	if c.Service != nil && c.Proto != ProtoICMP {
		return c.PortLabel() + " (" + c.Service.Name + ")"
	}
	if c.Proto == ProtoICMP {
		return "ICMP echo"
	}
	return c.PortLabel()
}

type Hop struct {
	TTL     int
	Addrs   []string
	Sent    int
	Recv    int
	RTTs    []float64
	Reply   string
	Reached bool
	Final   bool
	Done    bool
	SrcPort int
}

func (h *Hop) Stats() (min, avg, max, last float64, ok bool) {
	if len(h.RTTs) == 0 {
		return
	}
	min = h.RTTs[0]
	var sum float64
	for _, v := range h.RTTs {
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
		sum += v
	}
	return min, sum / float64(len(h.RTTs)), max, h.RTTs[len(h.RTTs)-1], true
}

func (h *Hop) Loss() float64 {
	if h.Sent == 0 {
		return 0
	}
	return float64(h.Sent-h.Recv) * 100 / float64(h.Sent)
}

func (h *Hop) clone() *Hop {
	c := *h
	c.Addrs = append([]string(nil), h.Addrs...)
	c.RTTs = append([]float64(nil), h.RTTs...)
	return &c
}

type Trace struct {
	mu      sync.RWMutex
	Cfg     Config
	DestIP  net.IP
	LocalIP net.IP
	Hops    []*Hop
	Status  string // idle | resolving | running | done | error | stopped
	Err     string
	Note    string
	Reached bool
	Started time.Time
	Ended   time.Time
	geo     *GeoResolver
}

type Snapshot struct {
	Cfg     Config
	DestIP  net.IP
	LocalIP net.IP
	Hops    []*Hop
	Status  string
	Err     string
	Note    string
	Reached bool
	Started time.Time
	Ended   time.Time
}

func NewTrace(cfg Config, geo *GeoResolver) *Trace {
	return &Trace{Cfg: cfg, geo: geo, Status: "idle"}
}

func (t *Trace) Snapshot() Snapshot {
	t.mu.RLock()
	defer t.mu.RUnlock()
	s := Snapshot{Cfg: t.Cfg, DestIP: t.DestIP, LocalIP: t.LocalIP, Status: t.Status, Err: t.Err,
		Note: t.Note, Reached: t.Reached, Started: t.Started, Ended: t.Ended}
	for _, h := range t.Hops {
		s.Hops = append(s.Hops, h.clone())
	}
	return s
}

func (t *Trace) Running() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.Status == "resolving" || t.Status == "running" || t.Status == "idle"
}

func (t *Trace) finish(status, errMsg string) {
	t.mu.Lock()
	t.Status = status
	if errMsg != "" {
		t.Err = errMsg
	}
	t.Ended = time.Now()
	t.mu.Unlock()
}

func resolveV4(host string) (net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			return v4, nil
		}
		return nil, errors.New("IPv6 targets are not supported yet")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("cannot resolve %q: %v", host, err)
	}
	for _, a := range ips {
		if v4 := a.IP.To4(); v4 != nil {
			return v4, nil
		}
	}
	return nil, fmt.Errorf("%q has no IPv4 address", host)
}

func localIPFor(dest net.IP) (net.IP, error) {
	c, err := net.Dial("udp4", net.JoinHostPort(dest.String(), "9"))
	if err != nil {
		return nil, err
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).IP.To4(), nil
}

// ------------------------------------------------------------------ engine

type engine struct {
	cfg     Config
	dest    net.IP
	local   net.IP
	conn    *net.IPConn
	lis     *listener
	payload []byte
	id      uint16
	seq     uint32
	port    uint32
}

type probeResult struct {
	From    net.IP
	Kind    string
	RTT     time.Duration
	OK      bool
	Reached bool
	Final   bool
	SrcPort int
}

func (t *Trace) Run(ctx context.Context) {
	t.mu.Lock()
	t.Status = "resolving"
	t.mu.Unlock()

	dest, err := resolveV4(t.Cfg.Target)
	if err != nil {
		t.finish("error", err.Error())
		return
	}
	local, err := localIPFor(dest)
	if err != nil || local == nil {
		t.finish("error", fmt.Sprintf("no route to %s: %v", dest, err))
		return
	}
	t.mu.Lock()
	t.DestIP, t.LocalIP, t.Started, t.Status = dest, local, time.Now(), "running"
	t.mu.Unlock()

	conn, err := net.ListenIP("ip4:icmp", &net.IPAddr{IP: local})
	if err != nil {
		t.finish("error", fmt.Sprintf("raw ICMP socket failed: %v (run as Administrator / root)", err))
		return
	}
	defer conn.Close()

	e := &engine{cfg: t.Cfg, dest: dest, local: local, conn: conn, lis: newListener(conn),
		payload: payloadFor(t.Cfg, dest), id: uint16(rand.Intn(65534) + 1), port: rand.Uint32() % 26000}

	silent := 0
	stoppedEarly := false
	for ttl := 1; ttl <= t.Cfg.MaxHops; ttl++ {
		if ctx.Err() != nil {
			t.finish("stopped", "")
			return
		}
		hop := &Hop{TTL: ttl, Sent: t.Cfg.Probes}
		t.mu.Lock()
		t.Hops = append(t.Hops, hop)
		t.mu.Unlock()

		e.runHop(ctx, t, hop)

		t.mu.Lock()
		hop.Done = true
		reached, final, recv, reply := hop.Reached, hop.Final, hop.Recv, hop.Reply
		t.mu.Unlock()

		if ctx.Err() != nil {
			t.finish("stopped", "")
			return
		}
		if reached || final {
			t.mu.Lock()
			t.Reached = reached
			if !reached {
				t.Note = "Path ended: " + reply
			}
			t.mu.Unlock()
			stoppedEarly = true
			break
		}
		if recv == 0 {
			silent++
		} else {
			silent = 0
		}
		if silent >= 6 {
			t.mu.Lock()
			n := len(t.Hops)
			for n > 0 && t.Hops[n-1].Recv == 0 {
				n--
			}
			if n > 0 {
				t.Hops = t.Hops[:n]
			}
			t.Note = "Destination did not answer (firewalled / drops these probes). Path shown up to the last responding hop."
			t.mu.Unlock()
			stoppedEarly = true
			break
		}
	}
	if !stoppedEarly {
		t.mu.Lock()
		t.Note = "Max hops reached without reaching the destination."
		t.mu.Unlock()
	}
	t.checkIntercept()
	t.finish("done", "")
}

// checkIntercept warns when the destination "answers" after very few hops but with a
// high RTT - the typical fingerprint of a VPN / proxy / TUN adapter that terminates the
// connection locally and resets the TTL, which makes a real traceroute impossible.
func (t *Trace) checkIntercept() {
	t.mu.Lock()
	defer t.mu.Unlock()
	n := len(t.Hops)
	if !t.Reached || n == 0 || n > 3 || ipClass(t.DestIP) != "" {
		return
	}
	mn, _, _, _, ok := t.Hops[n-1].Stats()
	if !ok || mn < 12 {
		return
	}
	t.Note = fmt.Sprintf("WARNING: destination answered at hop %d after %.0f ms - impossible for a real path. TTL is being reset by a VPN / proxy / NAT tunnel on this PC or on your gateway (local IP %s). The real route is hidden.", n, mn, t.LocalIP)
}

func (e *engine) runHop(ctx context.Context, t *Trace, hop *Hop) {
	if e.cfg.Proto == ProtoICMP {
		_ = setTTLConn(e.conn, hop.TTL)
	}
	var wg sync.WaitGroup
	for i := 0; i < e.cfg.Probes; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := e.probe(ctx, hop.TTL)
			t.record(hop, r)
		}()
	}
	wg.Wait()
}

func (t *Trace) record(hop *Hop, r probeResult) {
	t.mu.Lock()
	if r.SrcPort != 0 {
		hop.SrcPort = r.SrcPort
	}
	if r.OK {
		hop.Recv++
		hop.RTTs = append(hop.RTTs, float64(r.RTT.Microseconds())/1000)
		a := r.From.String()
		found := false
		for _, x := range hop.Addrs {
			if x == a {
				found = true
			}
		}
		if !found {
			hop.Addrs = append(hop.Addrs, a)
		}
		hop.Reply = r.Kind
		hop.Reached = hop.Reached || r.Reached
		hop.Final = hop.Final || r.Final
	}
	t.mu.Unlock()
	if r.OK && t.geo != nil {
		t.geo.Request(r.From.String())
	}
}

func (e *engine) probe(ctx context.Context, ttl int) probeResult {
	if nativeEchoAvailable {
		if e.cfg.Proto == ProtoICMP {
			return e.probeNative(ttl)
		}
		return e.probeHybrid(ctx, ttl)
	}
	switch e.cfg.Proto {
	case ProtoUDP:
		return e.probeUDP(ctx, ttl)
	case ProtoTCP:
		return e.probeTCP(ctx, ttl)
	}
	return e.probeICMP(ctx, ttl)
}

func (e *engine) nextPort() int {
	n := atomic.AddUint32(&e.port, 1)
	return 33434 + int(n%26000)
}

func classify(ev icmpEvent, dest net.IP) (kind string, reached, final bool) {
	switch ev.Type {
	case 0:
		return "Echo reply", true, false
	case 11:
		return "TTL exceeded", false, false
	case 3:
		switch ev.Code {
		case 3:
			return "Port unreachable", true, false
		case 0:
			return "!N Net unreachable", false, true
		case 1:
			return "!H Host unreachable", ev.From.Equal(dest), true
		case 2:
			return "Protocol unreachable", ev.From.Equal(dest), true
		case 4:
			return "Frag needed (DF)", false, false
		case 9, 10, 13:
			return "!X Admin prohibited", ev.From.Equal(dest), true
		}
		return fmt.Sprintf("Unreachable (code %d)", ev.Code), false, true
	case 12:
		return "Parameter problem", false, true
	}
	return fmt.Sprintf("ICMP %d/%d", ev.Type, ev.Code), false, false
}

func (e *engine) fromEvent(ev icmpEvent, start time.Time, sp int) probeResult {
	kind, reached, final := classify(ev, e.dest)
	return probeResult{From: ev.From, Kind: kind, RTT: ev.At.Sub(start), OK: true, Reached: reached, Final: final, SrcPort: sp}
}

func (e *engine) probeICMP(ctx context.Context, ttl int) probeResult {
	seq := uint16(atomic.AddUint32(&e.seq, 1))
	key := icmpKey{1, e.id, seq}
	ch := e.lis.register(key)
	defer e.lis.unregister(key)
	start := time.Now()
	if _, err := e.conn.WriteTo(buildEcho(e.id, seq), &net.IPAddr{IP: e.dest}); err != nil {
		return probeResult{}
	}
	timer := time.NewTimer(e.cfg.Timeout)
	defer timer.Stop()
	select {
	case ev := <-ch:
		return e.fromEvent(ev, start, 0)
	case <-timer.C:
	case <-ctx.Done():
	}
	return probeResult{}
}

func (e *engine) probeUDP(ctx context.Context, ttl int) probeResult {
	var pc *net.UDPConn
	sp := 0
	for try := 0; try < 8 && pc == nil; try++ {
		sp = e.nextPort()
		if c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: e.local, Port: sp}); err == nil {
			pc = c
		}
	}
	if pc == nil {
		return probeResult{}
	}
	defer pc.Close()
	if err := setTTLConn(pc, ttl); err != nil {
		return probeResult{}
	}
	key := icmpKey{17, uint16(sp), 0}
	ch := e.lis.register(key)
	defer e.lis.unregister(key)

	replyCh := make(chan time.Time, 1)
	_ = pc.SetReadDeadline(time.Now().Add(e.cfg.Timeout))
	go func() {
		buf := make([]byte, 2048)
		if _, _, err := pc.ReadFromUDP(buf); err == nil {
			replyCh <- time.Now()
		}
		// any error (timeout, closed, Windows WSAECONNRESET after ICMP unreachable)
		// simply ends the reader; the raw listener handles ICMP.
	}()

	start := time.Now()
	if _, err := pc.WriteToUDP(e.payload, &net.UDPAddr{IP: e.dest, Port: e.cfg.Port}); err != nil {
		return probeResult{SrcPort: sp}
	}
	timer := time.NewTimer(e.cfg.Timeout)
	defer timer.Stop()
	select {
	case ev := <-ch:
		return e.fromEvent(ev, start, sp)
	case at := <-replyCh:
		return probeResult{From: e.dest, Kind: "UDP reply", RTT: at.Sub(start), OK: true, Reached: true, SrcPort: sp}
	case <-timer.C:
	case <-ctx.Done():
	}
	return probeResult{SrcPort: sp}
}

func isRefused(err error) bool {
	var errno syscall.Errno
	if errors.As(err, &errno) && (errno == 10061 || errno == syscall.ECONNREFUSED) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "refused")
}

func (e *engine) probeTCP(ctx context.Context, ttl int) probeResult {
	sp := e.nextPort()
	key := icmpKey{6, uint16(sp), 0}
	ch := e.lis.register(key)
	defer e.lis.unregister(key)

	cctx, cancel := context.WithTimeout(ctx, e.cfg.Timeout)
	defer cancel()
	d := net.Dialer{
		LocalAddr: &net.TCPAddr{IP: e.local, Port: sp},
		Control: func(network, address string, c syscall.RawConn) error {
			var serr error
			if err := c.Control(func(fd uintptr) { serr = setTTLFd(fd, ttl) }); err != nil {
				return err
			}
			return serr
		},
	}
	resCh := make(chan error, 1)
	start := time.Now()
	go func() {
		conn, err := d.DialContext(cctx, "tcp4", net.JoinHostPort(e.dest.String(), strconv.Itoa(e.cfg.Port)))
		if conn != nil {
			conn.Close()
		}
		resCh <- err
	}()

	select {
	case ev := <-ch:
		return e.fromEvent(ev, start, sp)
	case err := <-resCh:
		rtt := time.Since(start)
		switch {
		case err == nil:
			return probeResult{From: e.dest, Kind: "SYN-ACK (open)", RTT: rtt, OK: true, Reached: true, SrcPort: sp}
		case isRefused(err):
			return probeResult{From: e.dest, Kind: "RST (closed)", RTT: rtt, OK: true, Reached: true, SrcPort: sp}
		}
		select { // an ICMP error may have raced the dial timeout
		case ev := <-ch:
			return e.fromEvent(ev, start, sp)
		default:
		}
		return probeResult{SrcPort: sp}
	}
}

// probeNative: ICMP echo through the OS API (Windows IcmpSendEcho, same as tracert).
func (e *engine) probeNative(ttl int) probeResult {
	from, status, rtt, ok := nativeEcho(e.dest, ttl, e.cfg.Timeout)
	if !ok {
		return probeResult{}
	}
	r := probeResult{From: from, RTT: rtt, OK: true}
	switch status {
	case 0:
		r.Kind, r.Reached = "Echo reply", true
	case 11013, 11014:
		r.Kind = "TTL exceeded"
	case 11003:
		r.Kind, r.Final = "!N Net unreachable", true
	case 11004:
		r.Kind, r.Final, r.Reached = "!H Host unreachable", true, from.Equal(e.dest)
	case 11005:
		r.Kind, r.Final = "Protocol unreachable", true
	case 11006:
		r.Kind, r.Reached = "Port unreachable", true
	default:
		return probeResult{}
	}
	return r
}

// probeHybrid (Windows, TCP/UDP modes): the real TCP/UDP probe and an ICMP echo with the same
// TTL run side by side. The ICMP echo identifies intermediate routers reliably; the TCP/UDP probe
// decides whether the service port itself answers at the destination.
func (e *engine) probeHybrid(ctx context.Context, ttl int) probeResult {
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	l4c := make(chan probeResult, 1)
	natc := make(chan probeResult, 1)
	go func() {
		if e.cfg.Proto == ProtoUDP {
			l4c <- e.probeUDP(cctx, ttl)
		} else {
			l4c <- e.probeTCP(cctx, ttl)
		}
	}()
	go func() { natc <- e.probeNative(ttl) }()

	var l4, nat probeResult
	gotL4, gotNat := false, false
	for !(gotL4 && gotNat) {
		select {
		case r := <-l4c:
			l4, gotL4 = r, true
			if r.OK {
				return r
			}
		case r := <-natc:
			nat, gotNat = r, true
			if r.OK && (!r.Reached || r.Final) {
				return r // intermediate router (or path-ending ICMP error)
			}
		case <-ctx.Done():
			return probeResult{}
		}
	}
	if nat.OK && nat.Reached {
		nat.Kind = fmt.Sprintf("Host up, %s no answer", e.cfg.Proto)
		nat.SrcPort = l4.SrcPort
		return nat
	}
	return l4
}
