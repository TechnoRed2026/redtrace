package main

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const version = "1.0.0"

type options struct {
	target                      string
	port                        int
	proto                       Proto
	protoSet                    bool
	service                     string
	maxHops, probes, timeout    int
	noTUI, list, help, elevated bool
}

const usageText = `RedTrace - traceroute for games & services (ASN / company / port / connection type)

Usage:
  redtrace                          open the interactive TUI
  redtrace <host> [options]         trace straight away
  redtrace mc.hypixel.net -s minecraft
  redtrace 1.2.3.4 -P udp -p 27015  (custom proto/port)

Options:
  -s, --service <name>    preset: minecraft, cs2, cs16, gta, fivem, samp, ts3, mumble, rust ...
  -p, --port <1-65535>    destination port
  -P, --proto <tcp|udp|icmp>
  -m, --max-hops <n>      default 30
  -q, --probes <n>        probes per hop, default 3
  -w, --timeout <ms>      per-probe timeout, default 1500
  -n, --no-tui            plain text report instead of the TUI
  -l, --list              list all service presets
  -h, --help              this help

TUI keys:  Tab panels | type to search services | Enter start | c copy report | s save | r rerun | q quit
Needs raw sockets: Windows = Administrator (auto UAC prompt), Linux = root.
`

func parseArgs(args []string) (*options, error) {
	o := &options{maxHops: 30, probes: 3, timeout: 1500}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") || a == "-" {
			if o.target != "" {
				return nil, fmt.Errorf("unexpected argument %q", a)
			}
			o.target = a
			continue
		}
		name := strings.TrimLeft(a, "-")
		val, hasVal := "", false
		if k := strings.Index(name, "="); k >= 0 {
			val, name, hasVal = name[k+1:], name[:k], true
		}
		getVal := func() (string, error) {
			if hasVal {
				return val, nil
			}
			if i+1 >= len(args) {
				return "", fmt.Errorf("option %q needs a value", a)
			}
			i++
			return args[i], nil
		}
		getInt := func(lo, hi int) (int, error) {
			v, err := getVal()
			if err != nil {
				return 0, err
			}
			n, err := strconv.Atoi(v)
			if err != nil || n < lo || n > hi {
				return 0, fmt.Errorf("option %q must be a number between %d and %d", a, lo, hi)
			}
			return n, nil
		}
		var err error
		switch name {
		case "h", "help", "?":
			o.help = true
		case "l", "list":
			o.list = true
		case "n", "no-tui", "cli":
			o.noTUI = true
		case "elevated":
			o.elevated = true
		case "p", "port":
			o.port, err = getInt(1, 65535)
		case "m", "max-hops":
			o.maxHops, err = getInt(1, 64)
		case "q", "probes":
			o.probes, err = getInt(1, 10)
		case "w", "timeout":
			o.timeout, err = getInt(100, 10000)
		case "s", "service":
			o.service, err = getVal()
		case "P", "proto", "protocol":
			var v string
			if v, err = getVal(); err == nil {
				switch strings.ToLower(v) {
				case "tcp":
					o.proto = ProtoTCP
				case "udp":
					o.proto = ProtoUDP
				case "icmp":
					o.proto = ProtoICMP
				default:
					err = fmt.Errorf("unknown protocol %q (tcp|udp|icmp)", v)
				}
				o.protoSet = true
			}
		default:
			err = fmt.Errorf("unknown option %q", a)
		}
		if err != nil {
			return nil, err
		}
	}
	return o, nil
}

func listServices() {
	grp := ""
	for _, s := range services {
		if s.Group != grp {
			grp = s.Group
			fmt.Printf("\n[%s]\n", grp)
		}
		port := strconv.Itoa(s.Port)
		if s.Proto == ProtoICMP {
			port = "-"
		}
		fmt.Printf("  %-36s %-5s %-6s  (%s)\n", s.Name, s.Proto, port, s.Alias)
	}
}

func checkRaw() error {
	c, err := net.ListenIP("ip4:icmp", &net.IPAddr{IP: net.IPv4zero})
	if err != nil {
		return err
	}
	c.Close()
	return nil
}

func pause(o *options) {
	if o.elevated {
		fmt.Print("\nPress Enter to exit...")
		bufio.NewReader(os.Stdin).ReadString('\n')
	}
}

func main() {
	o, err := parseArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		fmt.Fprintln(os.Stderr, "try: redtrace --help")
		os.Exit(2)
	}
	if o.help {
		fmt.Print(usageText)
		return
	}
	if o.list {
		listServices()
		return
	}

	// apply --service to the plain config (TUI does it itself in newUI)
	var svcSel *Service
	if o.service != "" {
		svcSel = FindService(o.service)
		if svcSel == nil {
			fmt.Fprintf(os.Stderr, "unknown service %q - run: redtrace --list\n", o.service)
			os.Exit(2)
		}
	}

	if err := checkRaw(); err != nil {
		if runtime.GOOS == "windows" && !o.elevated && relaunchElevated(os.Args[1:]) {
			return
		}
		fmt.Fprintf(os.Stderr, "error: cannot open raw ICMP socket: %v\n       %s\n", err, adminHint)
		pause(o)
		os.Exit(1)
	}

	geo := NewGeoResolver()

	if o.noTUI {
		if o.target == "" {
			fmt.Fprintln(os.Stderr, "error: --no-tui needs a target")
			os.Exit(2)
		}
		runCLI(o, svcSel, geo)
		pause(o)
		return
	}

	if err := runTUI(o, geo); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		pause(o)
		os.Exit(1)
	}
}

func buildConfig(o *options, svcSel *Service) Config {
	cfg := Config{Target: o.target, MaxHops: o.maxHops, Probes: o.probes, Timeout: time.Duration(o.timeout) * time.Millisecond}
	switch {
	case svcSel != nil:
		cfg.Service, cfg.Proto, cfg.Port = svcSel, svcSel.Proto, svcSel.Port
	case o.port != 0:
		cfg.Proto = ProtoTCP
	default:
		cfg.Proto = ProtoICMP
	}
	if o.port != 0 {
		cfg.Port = o.port
		cfg.Service = nil
	}
	if o.protoSet {
		cfg.Proto = o.proto
		if cfg.Port == 0 && o.proto != ProtoICMP {
			cfg.Port = 443
		}
	}
	if cfg.Proto == ProtoICMP {
		cfg.Port = 0
	}
	return cfg
}

func runCLI(o *options, svcSel *Service, geo *GeoResolver) {
	cfg := buildConfig(o, svcSel)
	ctx, cancel := context.WithCancel(context.Background())
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	go func() { <-sig; cancel() }()

	tr := NewTrace(cfg, geo)
	fmt.Fprintf(os.Stderr, "tracing %s with %s ...\n", cfg.Target, cfg.Label())
	done := make(chan struct{})
	go func() { tr.Run(ctx); close(done) }()
	<-done
	for i := 0; i < 60 && geo.Pending() > 0; i++ {
		time.Sleep(100 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond) // reverse-DNS stragglers
	fmt.Print(buildReport(tr.Snapshot(), geo))
}
