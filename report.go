package main

import (
	"fmt"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

func fmtMs(v float64) string {
	if v >= 100 {
		return fmt.Sprintf("%.0f ms", v)
	}
	return fmt.Sprintf("%.1f ms", v)
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func (s Snapshot) Elapsed() time.Duration {
	if s.Started.IsZero() {
		return 0
	}
	if s.Ended.IsZero() {
		return time.Since(s.Started)
	}
	return s.Ended.Sub(s.Started)
}

func buildReport(s Snapshot, geo *GeoResolver) string {
	var b strings.Builder
	fmt.Fprintf(&b, "RedTrace %s - traceroute report\n", version)
	dest := "?"
	if s.DestIP != nil {
		dest = s.DestIP.String()
	}
	fmt.Fprintf(&b, "Target  : %s (%s)\n", s.Cfg.Target, dest)
	fmt.Fprintf(&b, "Probe   : %s, %d probes/hop, timeout %v\n", s.Cfg.Label(), s.Cfg.Probes, s.Cfg.Timeout)
	if s.LocalIP != nil {
		fmt.Fprintf(&b, "Source  : %s\n", s.LocalIP)
	}
	res := "incomplete"
	if s.Reached {
		res = fmt.Sprintf("destination reached in %d hops", len(s.Hops))
	}
	fmt.Fprintf(&b, "Time    : %s   Result: %s (%s)\n", time.Now().Format("2006-01-02 15:04:05"), res, s.Status)
	if s.Err != "" {
		fmt.Fprintf(&b, "Error   : %s\n", s.Err)
	}
	if s.Note != "" {
		fmt.Fprintf(&b, "Note    : %s\n", s.Note)
	}
	b.WriteString("\n")
	tw := tabwriter.NewWriter(&b, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "TTL\tIP\tASN\tCOMPANY\tASN-NAME\tLOCATION\tTYPE\tPORT\tSRC\tLOSS\tBEST\tAVG\tLAST\tREPLY\tHOSTNAME")
	for _, h := range s.Hops {
		if len(h.Addrs) == 0 {
			fmt.Fprintf(tw, "%d\t*\t-\t-\t-\t-\t-\t%s\t-\t100%%\t-\t-\t-\tno reply\t-\n", h.TTL, s.Cfg.PortLabel())
			continue
		}
		g := geo.Get(h.Addrs[0])
		asn, comp, asname, loc, typ, host := "-", "-", "-", "-", "-", "-"
		if g != nil {
			asn, comp, asname = orDash(g.ASN), orDash(g.Company()), orDash(g.ASName)
			loc, typ, host = orDash(g.Location()), orDash(g.Type()), orDash(g.Reverse)
		}
		min, avg, _, last, _ := h.Stats()
		src := "-"
		if h.SrcPort != 0 {
			src = strconv.Itoa(h.SrcPort)
		}
		extra := ""
		if len(h.Addrs) > 1 {
			extra = "  (also: " + strings.Join(h.Addrs[1:], ",") + ")"
		}
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%.0f%%\t%s\t%s\t%s\t%s\t%s%s\n",
			h.TTL, h.Addrs[0], asn, comp, asname, loc, typ, s.Cfg.PortLabel(), src, h.Loss(),
			fmtMs(min), fmtMs(avg), fmtMs(last), h.Reply, host, extra)
	}
	tw.Flush()
	return b.String()
}
