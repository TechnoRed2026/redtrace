package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// ---------------------------------------------------------------- styling

const (
	cReset  = "\x1b[0m"
	cBold   = "\x1b[1m"
	cRed    = "\x1b[38;5;196m"
	cDim    = "\x1b[38;5;245m"
	cWhite  = "\x1b[97m"
	cGreen  = "\x1b[38;5;82m"
	cYellow = "\x1b[38;5;220m"
	cCyan   = "\x1b[38;5;45m"
	cOrange = "\x1b[38;5;208m"
	cMagent = "\x1b[38;5;177m"
	bgRed   = "\x1b[48;5;88m"
	bgSel   = "\x1b[48;5;52m"
)

func col(c, s string) string { return c + s + cReset }

func vlen(s string) int {
	n, esc := 0, false
	for _, r := range s {
		if esc {
			if r >= '@' && r <= '~' && r != '[' {
				esc = false
			}
			continue
		}
		if r == 0x1b {
			esc = true
			continue
		}
		n++
	}
	return n
}

func trunc(s string, w int) string {
	if w <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= w {
		return s
	}
	if w == 1 {
		return "…"
	}
	return string(r[:w-1]) + "…"
}

func padR(s string, w int) string {
	s = trunc(s, w)
	if n := vlen(s); n < w {
		s += strings.Repeat(" ", w-n)
	}
	return s
}

func padL(s string, w int) string {
	s = trunc(s, w)
	if n := vlen(s); n < w {
		s = strings.Repeat(" ", w-n) + s
	}
	return s
}

// ------------------------------------------------------------------ keys

type KeyKind int

const (
	KNone KeyKind = iota
	KRune
	KEnter
	KBackspace
	KTab
	KShiftTab
	KUp
	KDown
	KLeft
	KRight
	KHome
	KEnd
	KPgUp
	KPgDn
	KDel
	KEsc
	KCtrlC
	KCtrlU
	KF5
)

type Key struct {
	K KeyKind
	R rune
}

func csiKey(params string, final byte) Key {
	switch final {
	case 'A':
		return Key{K: KUp}
	case 'B':
		return Key{K: KDown}
	case 'C':
		return Key{K: KRight}
	case 'D':
		return Key{K: KLeft}
	case 'H':
		return Key{K: KHome}
	case 'F':
		return Key{K: KEnd}
	case 'Z':
		return Key{K: KShiftTab}
	case '~':
		switch params {
		case "3":
			return Key{K: KDel}
		case "1", "7":
			return Key{K: KHome}
		case "4", "8":
			return Key{K: KEnd}
		case "5":
			return Key{K: KPgUp}
		case "6":
			return Key{K: KPgDn}
		case "15":
			return Key{K: KF5}
		}
	}
	return Key{}
}

func parseKeys(b []byte) []Key {
	var ks []Key
	for i := 0; i < len(b); {
		c := b[i]
		switch {
		case c == 0x1b:
			if i+1 < len(b) && (b[i+1] == '[' || b[i+1] == 'O') {
				j := i + 2
				for j < len(b) && ((b[j] >= '0' && b[j] <= '9') || b[j] == ';') {
					j++
				}
				if j >= len(b) {
					i = len(b)
					continue
				}
				if k := csiKey(string(b[i+2:j]), b[j]); k.K != KNone {
					ks = append(ks, k)
				}
				i = j + 1
				continue
			}
			ks = append(ks, Key{K: KEsc})
			i++
		case c == 3:
			ks = append(ks, Key{K: KCtrlC})
			i++
		case c == '\r' || c == '\n':
			ks = append(ks, Key{K: KEnter})
			i++
		case c == 0x7f || c == 8:
			ks = append(ks, Key{K: KBackspace})
			i++
		case c == 9:
			ks = append(ks, Key{K: KTab})
			i++
		case c == 21:
			ks = append(ks, Key{K: KCtrlU})
			i++
		case c >= 0x20 && c < 0x7f:
			ks = append(ks, Key{K: KRune, R: rune(c)})
			i++
		default:
			i++
		}
	}
	return ks
}

func readKeys(out chan<- Key) {
	buf := make([]byte, 128)
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil || n == 0 {
			if err != nil {
				return
			}
			continue
		}
		for _, k := range parseKeys(buf[:n]) {
			out <- k
		}
	}
}

// -------------------------------------------------------------------- UI

const (
	scrSetup = 0
	scrTrace = 1
)

type UI struct {
	w, h   int
	scr    int
	foc    int // 0 target, 1 services, 2 options
	target string
	query  string
	list   []*Service
	svcIdx int
	svcTop int
	optIdx int

	portStr   string
	proto     Proto
	custom    bool
	hops      int
	probes    int
	timeoutMs int
	svc       *Service

	msg   string
	msgAt time.Time

	tr     *Trace
	cancel context.CancelFunc
	sel    int
	top    int
	follow bool
	frame  int
	geo    *GeoResolver
	quit   bool
}

func newUI(o *options, geo *GeoResolver) *UI {
	u := &UI{geo: geo, list: services, hops: o.maxHops, probes: o.probes, timeoutMs: o.timeout, target: o.target}
	u.applySvc()
	if o.service != "" {
		if s := FindService(o.service); s != nil {
			u.query = ""
			for i, x := range u.list {
				if x == s {
					u.svcIdx = i
				}
			}
			u.applySvc()
		}
	}
	if o.protoSet {
		u.proto, u.custom = o.proto, true
	}
	if o.port != 0 {
		u.portStr, u.custom = strconv.Itoa(o.port), true
	}
	return u
}

func (u *UI) flash(s string) { u.msg, u.msgAt = s, time.Now() }

func (u *UI) applySvc() {
	if u.svcIdx >= len(u.list) {
		u.svcIdx = len(u.list) - 1
	}
	if u.svcIdx < 0 || len(u.list) == 0 {
		u.svc = nil
		return
	}
	u.svc = u.list[u.svcIdx]
	u.portStr = strconv.Itoa(u.svc.Port)
	if u.svc.Proto == ProtoICMP {
		u.portStr = "0"
	}
	u.proto = u.svc.Proto
	u.custom = false
}

func (u *UI) refilter() {
	u.list = filterServices(u.query)
	u.svcIdx, u.svcTop = 0, 0
	u.applySvc()
}

func (u *UI) stopTrace() {
	if u.cancel != nil {
		u.cancel()
		u.cancel = nil
	}
}

func (u *UI) start() {
	t := strings.TrimSpace(u.target)
	if t == "" {
		u.foc = 0
		u.flash("Enter a target host or IP first")
		return
	}
	port, _ := strconv.Atoi(u.portStr)
	if u.proto != ProtoICMP && (port < 1 || port > 65535) {
		u.foc, u.optIdx = 2, 0
		u.flash("Invalid port (1-65535)")
		return
	}
	cfg := Config{Target: t, Port: port, Proto: u.proto, MaxHops: u.hops, Probes: u.probes,
		Timeout: time.Duration(u.timeoutMs) * time.Millisecond}
	if u.proto == ProtoICMP {
		cfg.Port = 0
	}
	if !u.custom && u.svc != nil {
		cfg.Service = u.svc
	}
	u.stopTrace()
	ctx, cancel := context.WithCancel(context.Background())
	u.cancel = cancel
	u.tr = NewTrace(cfg, u.geo)
	go u.tr.Run(ctx)
	u.scr, u.follow, u.sel, u.top = scrTrace, true, 0, 0
}

func (u *UI) handleKey(k Key) {
	if k.K == KCtrlC {
		u.quit = true
		return
	}
	if u.scr == scrTrace {
		u.keyTrace(k)
		return
	}
	switch k.K {
	case KEsc:
		if u.foc == 1 && u.query != "" {
			u.query = ""
			u.refilter()
			return
		}
		u.quit = true
		return
	case KTab:
		u.foc = (u.foc + 1) % 3
		return
	case KShiftTab:
		u.foc = (u.foc + 2) % 3
		return
	case KEnter, KF5:
		u.start()
		return
	}
	switch u.foc {
	case 0:
		u.keyTarget(k)
	case 1:
		u.keyService(k)
	default:
		u.keyOptions(k)
	}
}

func (u *UI) keyTarget(k Key) {
	switch k.K {
	case KRune:
		if r := k.R; unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune(".-_:", r) {
			u.target += string(r)
		}
	case KBackspace:
		if r := []rune(u.target); len(r) > 0 {
			u.target = string(r[:len(r)-1])
		}
	case KCtrlU:
		u.target = ""
	case KDown:
		u.foc = 1
	}
}

func (u *UI) keyService(k Key) {
	move := func(d int) {
		u.svcIdx += d
		if u.svcIdx < 0 {
			u.svcIdx = 0
		}
		if u.svcIdx >= len(u.list) {
			u.svcIdx = len(u.list) - 1
		}
		u.applySvc()
	}
	switch k.K {
	case KRune:
		u.query += string(k.R)
		u.refilter()
	case KBackspace:
		if r := []rune(u.query); len(r) > 0 {
			u.query = string(r[:len(r)-1])
			u.refilter()
		}
	case KCtrlU:
		u.query = ""
		u.refilter()
	case KUp:
		if u.svcIdx == 0 {
			u.foc = 0
		} else {
			move(-1)
		}
	case KDown:
		move(1)
	case KPgUp:
		move(-10)
	case KPgDn:
		move(10)
	case KHome:
		move(-1 << 20)
	case KEnd:
		move(1 << 20)
	case KRight:
		u.foc = 2
	}
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func (u *UI) adjust(d int) {
	switch u.optIdx {
	case 0:
		p, _ := strconv.Atoi(u.portStr)
		u.portStr = strconv.Itoa(clampInt(p+d, 1, 65535))
		u.custom = true
	case 1:
		if d > 0 {
			u.proto = u.proto.Next()
		} else {
			u.proto = u.proto.Prev()
		}
		if u.proto != ProtoICMP {
			if p, _ := strconv.Atoi(u.portStr); p < 1 {
				u.portStr = "443"
			}
		}
		u.custom = true
	case 2:
		u.hops = clampInt(u.hops+d, 1, 64)
	case 3:
		u.probes = clampInt(u.probes+d, 1, 10)
	case 4:
		u.timeoutMs = clampInt(u.timeoutMs+250*d, 250, 10000)
	}
}

func (u *UI) keyOptions(k Key) {
	switch k.K {
	case KUp:
		if u.optIdx == 0 {
			u.foc = 1
		} else {
			u.optIdx--
		}
	case KDown:
		u.optIdx = clampInt(u.optIdx+1, 0, 4)
	case KLeft:
		u.adjust(-1)
	case KRight:
		u.adjust(1)
	case KRune:
		switch {
		case k.R == '+':
			u.adjust(1)
		case k.R == '-':
			u.adjust(-1)
		case u.optIdx == 0 && k.R >= '0' && k.R <= '9':
			if u.portStr == "0" || u.custom == false {
				u.portStr = ""
			}
			if len(u.portStr) < 5 {
				u.portStr += string(k.R)
			}
			u.custom = true
		}
	case KBackspace:
		if u.optIdx == 0 && len(u.portStr) > 0 {
			u.portStr = u.portStr[:len(u.portStr)-1]
			u.custom = true
		}
	}
}

func (u *UI) keyTrace(k Key) {
	switch k.K {
	case KEsc:
		u.stopTrace()
		u.scr = scrSetup
	case KUp:
		u.follow = false
		u.sel--
	case KDown:
		u.follow = false
		u.sel++
	case KPgUp:
		u.follow = false
		u.sel -= 10
	case KPgDn:
		u.follow = false
		u.sel += 10
	case KHome:
		u.follow = false
		u.sel = 0
	case KEnd:
		u.follow = true
	case KF5:
		u.start()
	case KRune:
		switch unicode.ToLower(k.R) {
		case 'q':
			u.quit = true
		case 'c':
			u.copyReport()
		case 's':
			u.saveReport()
		case 'r':
			u.start()
		case 'e':
			u.stopTrace()
			u.scr = scrSetup
		case 'f':
			u.follow = !u.follow
		}
	}
}

func (u *UI) report() string {
	if u.tr == nil {
		return ""
	}
	// give geo lookups a moment so the report is complete
	for i := 0; i < 20 && u.geo.Pending() > 0; i++ {
		time.Sleep(100 * time.Millisecond)
	}
	return buildReport(u.tr.Snapshot(), u.geo)
}

func (u *UI) copyReport() {
	if err := copyToClipboard(u.report()); err != nil {
		u.flash("Copy failed: " + err.Error())
		return
	}
	u.flash("Report copied to clipboard")
}

func sanitize(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	return b.String()
}

func (u *UI) saveReport() {
	if u.tr == nil {
		return
	}
	name := fmt.Sprintf("redtrace_%s_%s.txt", sanitize(u.tr.Cfg.Target), time.Now().Format("20060102_150405"))
	if err := os.WriteFile(name, []byte(u.report()), 0o644); err != nil {
		u.flash("Save failed: " + err.Error())
		return
	}
	abs, _ := filepath.Abs(name)
	u.flash("Saved: " + abs)
}

// ------------------------------------------------------------------ main loop

func runTUI(o *options, geo *GeoResolver) error {
	restore, err := termInit()
	if err != nil {
		return fmt.Errorf("cannot init terminal: %v (use --no-tui)", err)
	}
	defer restore()
	out := bufio.NewWriterSize(os.Stdout, 1<<16)
	fmt.Fprint(out, "\x1b[?1049h\x1b[?25l\x1b[?7l")
	out.Flush()
	defer fmt.Fprint(os.Stdout, "\x1b[?7h\x1b[?25h\x1b[?1049l"+cReset)

	keys := make(chan Key, 64)
	go readKeys(keys)

	u := newUI(o, geo)
	if o.target != "" {
		u.start()
	}
	tick := time.NewTicker(120 * time.Millisecond)
	defer tick.Stop()
	for !u.quit {
		u.w, u.h = termSize()
		u.frame++
		u.draw(out)
		select {
		case k := <-keys:
			u.handleKey(k)
			for more := true; more && !u.quit; {
				select {
				case k2 := <-keys:
					u.handleKey(k2)
				default:
					more = false
				}
			}
		case <-tick.C:
		}
	}
	u.stopTrace()
	return nil
}

func (u *UI) draw(out *bufio.Writer) {
	var lines []string
	switch {
	case u.w < 70 || u.h < 14:
		lines = []string{col(cRed, "Terminal too small - need at least 70x14 (now "+strconv.Itoa(u.w)+"x"+strconv.Itoa(u.h)+")")}
	case u.scr == scrSetup:
		lines = u.setupLines()
	default:
		lines = u.traceLines()
	}
	if len(lines) > u.h {
		lines = lines[:u.h]
	}
	fmt.Fprint(out, "\x1b[H"+strings.Join(lines, "\x1b[K\r\n")+"\x1b[K\x1b[J")
	out.Flush()
}

// ------------------------------------------------------------- setup screen

func (u *UI) banner() string {
	return " " + cRed + cBold + "▓▒░ REDTRACE ░▒▓" + cReset + cDim + "  traceroute for games & services · IP · ASN · company · port · connection type   v" + version + cReset
}

func (u *UI) footer(hint string) []string {
	l := " " + cDim + hint + cReset
	m := ""
	if u.msg != "" && time.Since(u.msgAt) < 5*time.Second {
		m = " " + col(cYellow, u.msg)
	}
	return []string{l, m}
}

func (u *UI) setupLines() []string {
	W, H := u.w, u.h
	var L []string
	L = append(L, u.banner(), "")

	fc := cDim
	if u.foc == 0 {
		fc = cRed
	}
	tg := u.target
	if u.foc == 0 {
		tg += "▌"
	}
	L = append(L, " "+cDim+"Target   "+cReset+col(fc, "[ ")+padR(tg, 42)+col(fc, " ]")+cDim+"  hostname or IP, e.g. mc.hypixel.net / 1.1.1.1"+cReset)
	L = append(L, "")

	LW := clampInt(W*58/100, 44, 74)
	RW := W - LW - 4
	listRows := H - 10
	if listRows < 3 {
		listRows = 3
	}
	if u.svcIdx < u.svcTop {
		u.svcTop = u.svcIdx
	}
	if u.svcIdx >= u.svcTop+listRows {
		u.svcTop = u.svcIdx - listRows + 1
	}

	q := u.query
	if u.foc == 1 {
		q += "▌"
	}
	lt := padR(" SERVICES   search: "+q, LW)
	rt := padR(" SETTINGS", RW)
	if u.foc == 1 {
		lt = cRed + cBold + lt + cReset
	} else {
		lt = cDim + cBold + lt + cReset
	}
	if u.foc == 2 {
		rt = cRed + cBold + rt + cReset
	} else {
		rt = cDim + cBold + rt + cReset
	}
	L = append(L, " "+lt+cDim+" │ "+cReset+rt)
	L = append(L, " "+cDim+strings.Repeat("─", LW)+"─┼─"+strings.Repeat("─", RW-1)+cReset)

	// right-hand rows
	opt := func(i int, label, val string) string {
		line := " " + padR(label, 11) + padR(val, RW-13)
		if u.foc == 2 && u.optIdx == i {
			return bgRed + cWhite + padR(line, RW) + cReset
		}
		return cDim + " " + padR(label, 11) + cReset + padR(val, RW-13)
	}
	portVal := u.portStr
	if u.proto == ProtoICMP {
		portVal = "n/a (ICMP)"
	} else if u.foc == 2 && u.optIdx == 0 {
		portVal += "▌"
	}
	if u.custom && u.proto != ProtoICMP {
		portVal += "  (custom)"
	}
	right := []string{
		opt(0, "Port", portVal),
		opt(1, "Protocol", "‹ "+u.proto.String()+" ›"),
		opt(2, "Max hops", strconv.Itoa(u.hops)),
		opt(3, "Probes/hop", strconv.Itoa(u.probes)),
		opt(4, "Timeout", strconv.Itoa(u.timeoutMs)+" ms"),
		"",
	}
	sel := "Custom port"
	if !u.custom && u.svc != nil {
		sel = u.svc.Name
	}
	right = append(right, " "+cDim+"Selected"+cReset, " "+col(cWhite+cBold, trunc(sel, RW-2)))
	switch {
	case u.proto == ProtoTCP:
		right = append(right, " "+cDim+"TCP SYN probes (connect, no data)"+cReset)
	case u.proto == ProtoUDP:
		var s *Service
		if !u.custom {
			s = u.svc
		} else if p, _ := strconv.Atoi(u.portStr); p > 0 {
			s = ServiceByPort(ProtoUDP, p)
		}
		if s != nil && s.Payload != nil {
			right = append(right, " "+cDim+"UDP probes carry a real query for"+cReset, " "+cDim+"this service (server will answer)"+cReset)
		} else {
			right = append(right, " "+cDim+"UDP probes carry a generic payload"+cReset)
		}
	default:
		right = append(right, " "+cDim+"ICMP echo probes (classic)"+cReset)
	}
	right = append(right, "", " "+cDim+"Result PORT column = proto:port"+cReset, " "+cDim+"the probes used on every hop."+cReset)

	showGrp := LW >= 60
	for i := 0; i < listRows; i++ {
		left := strings.Repeat(" ", LW)
		idx := u.svcTop + i
		if len(u.list) == 0 && i == 0 {
			left = padR(" "+col(cYellow, "no service matches - type a custom port in SETTINGS"), LW)
		} else if idx < len(u.list) {
			s := u.list[idx]
			nameW := LW - 2 - 1 - 4 - 1 - 5
			if showGrp {
				nameW -= 11
			}
			port := strconv.Itoa(s.Port)
			if s.Proto == ProtoICMP {
				port = "-"
			}
			txt := padR(s.Name, nameW) + " " + padR(s.Proto.String(), 4) + " " + padL(port, 5)
			if showGrp {
				txt += " " + padR(s.Group, 10)
			}
			if idx == u.svcIdx {
				if u.foc == 1 {
					left = bgRed + cWhite + "▸ " + txt + cReset
				} else {
					left = cRed + "▸ " + cReset + cWhite + txt + cReset
				}
			} else {
				left = "  " + txt
			}
			if vlen(left) < LW {
				left += strings.Repeat(" ", LW-vlen(left))
			}
		}
		r := ""
		if i < len(right) {
			r = right[i]
		}
		L = append(L, " "+left+cDim+" │ "+cReset+r)
	}
	L = append(L, "")
	L = append(L, u.footer("Tab/Shift-Tab panels · ↑↓ choose · type to search · ←→ change value · Enter/F5 start · Esc quit")...)
	return L
}

// ------------------------------------------------------------- trace screen

type colDef struct {
	id    string
	title string
	w     int
	prio  int
	right bool
}

var traceCols = []colDef{
	{"ttl", "#", 3, 0, true},
	{"ip", "IP ADDRESS", 16, 0, false},
	{"asn", "ASN", 9, 0, false},
	{"company", "COMPANY", 18, 0, false},
	{"loc", "LOCATION", 16, 3, false},
	{"type", "TYPE", 11, 2, false},
	{"pp", "PORT", 9, 4, false},
	{"loss", "LOSS", 5, 0, true},
	{"best", "BEST", 8, 6, true},
	{"avg", "AVG", 8, 0, true},
	{"last", "LAST", 8, 7, true},
	{"reply", "REPLY", 16, 5, false},
	{"host", "HOSTNAME", 24, 8, false},
}

func fitCols(W int) []colDef {
	avail := W - 2
	use := map[string]bool{}
	total := -1
	for _, c := range traceCols {
		if c.prio == 0 {
			use[c.id] = true
			total += c.w + 1
		}
	}
	for p := 1; p <= 8; p++ {
		for _, c := range traceCols {
			if c.prio == p && total+c.w+1 <= avail {
				use[c.id] = true
				total += c.w + 1
			}
		}
	}
	extra := avail - total
	var out []colDef
	for _, c := range traceCols {
		if !use[c.id] {
			continue
		}
		if c.id == "company" && extra > 0 {
			d := clampInt(extra, 0, 18)
			c.w += d
			extra -= d
		}
		out = append(out, c)
	}
	for i := range out {
		if out[i].id == "host" && extra > 0 {
			out[i].w += clampInt(extra, 0, 24)
		}
	}
	return out
}

func rttColor(ms float64) string {
	switch {
	case ms < 50:
		return cGreen
	case ms < 120:
		return cYellow
	}
	return cRed
}

func typeColor(t string) string {
	switch t {
	case "Datacenter":
		return cYellow
	case "Mobile":
		return cMagent
	case "VPN/Proxy":
		return cRed
	case "ISP/Transit":
		return cCyan
	}
	return cDim
}

func (u *UI) cell(id string, h *Hop, g *Geo, cfg Config) (string, string) {
	spin := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}[u.frame%10]
	has := len(h.Addrs) > 0
	switch id {
	case "ttl":
		return strconv.Itoa(h.TTL), cDim
	case "ip":
		if !has {
			if !h.Done {
				return spin, cYellow
			}
			return "*", cDim
		}
		s := h.Addrs[0]
		if len(h.Addrs) > 1 {
			s += "+"
		}
		if h.Reached {
			return s, cGreen + cBold
		}
		return s, cWhite
	case "asn":
		if !has {
			return "", cDim
		}
		if g == nil {
			return "…", cDim
		}
		if g.Class != "" {
			return "LAN", cDim
		}
		return orDash(g.ASN), cCyan
	case "company":
		if !has {
			if h.Done {
				return "no reply (ICMP filtered)", cDim
			}
			return "", cDim
		}
		if g == nil {
			return "resolving…", cDim
		}
		return orDash(g.Company()), cWhite
	case "loc":
		return g.Location(), cDim
	case "type":
		return g.Type(), typeColor(g.Type())
	case "pp":
		return cfg.PortLabel(), cOrange
	case "loss":
		if !h.Done {
			return "…", cDim
		}
		l := h.Loss()
		c := cGreen
		if l > 0 {
			c = cYellow
		}
		if l >= 50 {
			c = cRed
		}
		return fmt.Sprintf("%.0f%%", l), c
	case "best", "avg", "last":
		mn, av, _, la, ok := h.Stats()
		if !ok {
			return "", cDim
		}
		v := map[string]float64{"best": mn, "avg": av, "last": la}[id]
		return fmtMs(v), rttColor(v)
	case "reply":
		return h.Reply, cDim
	case "host":
		if g != nil {
			return g.Reverse, cDim
		}
	}
	return "", cDim
}

func (u *UI) traceLines() []string {
	W, H := u.w, u.h
	if u.tr == nil {
		return []string{"no trace"}
	}
	s := u.tr.Snapshot()
	var L []string

	status := col(cYellow, "● resolving")
	switch s.Status {
	case "running":
		status = col(cYellow, []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}[u.frame%10]+" tracing")
	case "done":
		if s.Reached {
			status = col(cGreen, "✔ reached")
		} else {
			status = col(cOrange, "■ finished")
		}
	case "error":
		status = col(cRed, "✖ error")
	case "stopped":
		status = col(cDim, "■ stopped")
	}
	dest := "…"
	if s.DestIP != nil {
		dest = s.DestIP.String()
	}
	L = append(L, " "+cRed+cBold+"▓▒░ REDTRACE ░▒▓"+cReset+"  "+col(cWhite+cBold, s.Cfg.Target)+cDim+" → "+cReset+col(cCyan, dest)+"   "+status)
	info := fmt.Sprintf(" %s%s%s  ·  hops %d/%d  ·  %d probes  ·  %.1fs", cOrange, s.Cfg.Label(), cDim, len(s.Hops), s.Cfg.MaxHops, s.Cfg.Probes, s.Elapsed().Seconds())
	if s.LocalIP != nil {
		info += "  ·  from " + s.LocalIP.String()
	}
	L = append(L, info+cReset)
	if s.Err != "" {
		L = append(L, " "+col(cRed, trunc(s.Err, W-2)))
	} else if s.Note != "" {
		L = append(L, " "+col(cYellow, trunc(s.Note, W-2)))
		if len([]rune(s.Note)) > W-2 {
			L[len(L)-1] = " " + col(cYellow, string([]rune(s.Note)[:W-2]))
			L = append(L, " "+col(cYellow, trunc(string([]rune(s.Note)[W-2:]), W-2)))
		}
	} else {
		L = append(L, "")
	}

	cols := fitCols(W)
	var hdr strings.Builder
	hdr.WriteString(" ")
	for i, c := range cols {
		if i > 0 {
			hdr.WriteString(" ")
		}
		if c.right {
			hdr.WriteString(padL(c.title, c.w))
		} else {
			hdr.WriteString(padR(c.title, c.w))
		}
	}
	L = append(L, cRed+cBold+hdr.String()+cReset)
	L = append(L, " "+cDarkLine(W-2))

	n := len(s.Hops)
	if u.follow {
		u.sel = n - 1
	}
	u.sel = clampInt(u.sel, 0, maxInt(n-1, 0))
	rows := H - 15
	if rows < 3 {
		rows = 3
	}
	if u.sel < u.top {
		u.top = u.sel
	}
	if u.sel >= u.top+rows {
		u.top = u.sel - rows + 1
	}
	if u.top < 0 {
		u.top = 0
	}

	for i := 0; i < rows; i++ {
		idx := u.top + i
		if idx >= n {
			L = append(L, "")
			continue
		}
		h := s.Hops[idx]
		var g *Geo
		if len(h.Addrs) > 0 {
			g = u.geo.Get(h.Addrs[0])
		}
		bg := ""
		if idx == u.sel {
			bg = bgSel
		}
		var sb strings.Builder
		sb.WriteString(bg + " " + cReset)
		for ci, c := range cols {
			if ci > 0 {
				sb.WriteString(bg + " " + cReset)
			}
			txt, clr := u.cell(c.id, h, g, s.Cfg)
			if c.right {
				txt = padL(txt, c.w)
			} else {
				txt = padR(txt, c.w)
			}
			sb.WriteString(bg + clr + txt + cReset)
		}
		L = append(L, sb.String())
	}

	// details
	L = append(L, " "+cDarkLine(W-2))
	L = append(L, u.detailLines(s, W)...)
	L = append(L, u.footer("↑↓ select hop · c copy report · s save .txt · r rerun · e edit · f follow · q quit")...)
	return L
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func cDarkLine(w int) string {
	return cDim + strings.Repeat("─", maxInt(w, 0)) + cReset
}

func (u *UI) detailLines(s Snapshot, W int) []string {
	lines := make([]string, 5)
	if len(s.Hops) == 0 || u.sel >= len(s.Hops) {
		return lines
	}
	h := s.Hops[u.sel]
	lbl := func(k string) string { return cDim + k + " " + cReset }
	if len(h.Addrs) == 0 {
		lines[0] = " " + lbl("Hop") + col(cWhite, strconv.Itoa(h.TTL)) + cDim + "  no response - router filters/rate-limits ICMP or the host is silent" + cReset
		lines[3] = " " + lbl("Probe") + col(cOrange, s.Cfg.PortLabel()) + cDim + fmt.Sprintf("  src port %d → dst port %d", h.SrcPort, s.Cfg.Port) + cReset
		return lines
	}
	g := u.geo.Get(h.Addrs[0])
	host := ""
	if g != nil && g.Reverse != "" {
		host = "  " + cDim + g.Reverse + cReset
	}
	lines[0] = " " + lbl("Hop") + col(cWhite+cBold, strconv.Itoa(h.TTL)) + "  " + col(cCyan, strings.Join(h.Addrs, ", ")) + host
	if g == nil {
		lines[1] = " " + cDim + "looking up ASN / company…" + cReset
	} else {
		lines[1] = " " + lbl("ASN") + col(cCyan, orDash(g.ASN)) + "  " + lbl("ASN-name") + orDash(g.ASName) + "  " + lbl("Company") + col(cWhite, orDash(g.Company())) + "  " + lbl("ISP") + orDash(g.ISP)
		loc := orDash(g.Location())
		if g.Country != "" {
			loc = g.Country + " (" + g.CC + ")"
			if g.Region != "" {
				loc += ", " + g.Region
			}
			if g.City != "" {
				loc += ", " + g.City
			}
		}
		lines[2] = " " + lbl("Location") + loc + "  " + lbl("Connection") + col(typeColor(g.Type()), g.Type()) + "  " + cDim + "[" + g.Source + "]" + cReset
	}
	src := "-"
	if h.SrcPort != 0 {
		src = strconv.Itoa(h.SrcPort)
	}
	lines[3] = " " + lbl("Transport") + col(cOrange, s.Cfg.PortLabel()) + cDim + "  src port " + src + " → dst port " + strconv.Itoa(s.Cfg.Port) + cReset + "  " + lbl("Reply") + h.Reply
	mn, av, mx, la, ok := h.Stats()
	if ok {
		lines[4] = " " + lbl("RTT") + fmt.Sprintf("best %s · avg %s · worst %s · last %s", fmtMs(mn), fmtMs(av), fmtMs(mx), fmtMs(la)) + "  " + lbl("Loss") + fmt.Sprintf("%.0f%% (%d/%d)", h.Loss(), h.Recv, h.Sent)
	}
	for i := range lines {
		if vlen(lines[i]) > W-1 {
			// crude safe truncation of styled text: strip colors when too long
			lines[i] = trunc(stripANSI(lines[i]), W-1)
		}
	}
	return lines
}

func stripANSI(s string) string {
	var b strings.Builder
	esc := false
	for _, r := range s {
		if esc {
			if r >= '@' && r <= '~' && r != '[' {
				esc = false
			}
			continue
		}
		if r == 0x1b {
			esc = true
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
