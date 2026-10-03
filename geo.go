package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Geo holds everything we know about one hop IP.
type Geo struct {
	IP, ASN, ASName, Org, ISP string
	Country, CC, Region, City string
	Reverse, Class, Source    string
	Mobile, Proxy, Hosting    bool
}

func (g *Geo) Company() string {
	if g == nil {
		return ""
	}
	switch g.Class {
	case "Private LAN":
		return "Private network (RFC1918)"
	case "CGNAT":
		return "Carrier-grade NAT (ISP)"
	case "Loopback":
		return "Localhost"
	case "":
	default:
		return g.Class
	}
	switch {
	case g.Org != "":
		return g.Org
	case g.ISP != "":
		return g.ISP
	}
	return g.ASName
}

func (g *Geo) Location() string {
	if g == nil || g.Class != "" {
		return ""
	}
	var p []string
	if g.CC != "" {
		p = append(p, g.CC)
	}
	if g.City != "" {
		p = append(p, g.City)
	} else if g.Region != "" {
		p = append(p, g.Region)
	}
	return strings.Join(p, " ")
}

func (g *Geo) Type() string {
	if g == nil {
		return ""
	}
	switch {
	case g.Class != "":
		return g.Class
	case g.Proxy:
		return "VPN/Proxy"
	case g.Hosting:
		return "Datacenter"
	case g.Mobile:
		return "Mobile"
	case g.ASN != "":
		return "ISP/Transit"
	}
	return "-"
}

func ipClass(ip net.IP) string {
	switch {
	case ip.IsLoopback():
		return "Loopback"
	case ip.IsPrivate():
		return "Private LAN"
	case ip.IsLinkLocalUnicast():
		return "Link-local"
	case ip.IsUnspecified():
		return "Unspecified"
	}
	if v4 := ip.To4(); v4 != nil {
		if v4[0] == 100 && v4[1]&0xc0 == 64 {
			return "CGNAT"
		}
		if v4[0] >= 224 {
			return "Reserved"
		}
	}
	return ""
}

type GeoResolver struct {
	mu           sync.Mutex
	cache        map[string]*Geo
	queued       map[string]bool
	queue        []string
	pending      int
	kick         chan struct{}
	limitedUntil time.Time
	client       *http.Client
}

func NewGeoResolver() *GeoResolver {
	g := &GeoResolver{
		cache:  map[string]*Geo{},
		queued: map[string]bool{},
		kick:   make(chan struct{}, 1),
		client: &http.Client{Timeout: 3 * time.Second},
	}
	go g.worker()
	return g
}

func (g *GeoResolver) Get(ip string) *Geo {
	g.mu.Lock()
	defer g.mu.Unlock()
	if v := g.cache[ip]; v != nil {
		c := *v
		return &c
	}
	return nil
}

// Pending returns how many IPs are still waiting for a lookup.
func (g *GeoResolver) Pending() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.pending
}

func (g *GeoResolver) Request(ip string) {
	p := net.ParseIP(ip)
	if p == nil {
		return
	}
	g.mu.Lock()
	if g.cache[ip] != nil || g.queued[ip] {
		g.mu.Unlock()
		return
	}
	if cls := ipClass(p); cls != "" {
		g.cache[ip] = &Geo{IP: ip, Class: cls, Source: "local"}
		g.mu.Unlock()
		go g.rdnsFill(ip)
		return
	}
	g.queued[ip] = true
	g.queue = append(g.queue, ip)
	g.pending++
	g.mu.Unlock()
	select {
	case g.kick <- struct{}{}:
	default:
	}
}

func (g *GeoResolver) rdnsFill(ip string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	names, err := net.DefaultResolver.LookupAddr(ctx, ip)
	if err != nil || len(names) == 0 {
		return
	}
	g.mu.Lock()
	if v := g.cache[ip]; v != nil && v.Reverse == "" {
		v.Reverse = strings.TrimSuffix(names[0], ".")
	}
	g.mu.Unlock()
}

func (g *GeoResolver) worker() {
	for range g.kick {
		time.Sleep(250 * time.Millisecond) // let a few hops accumulate
		for {
			g.mu.Lock()
			n := len(g.queue)
			if n > 100 {
				n = 100
			}
			batch := append([]string(nil), g.queue[:n]...)
			g.queue = g.queue[n:]
			g.mu.Unlock()
			if len(batch) == 0 {
				break
			}
			g.resolveBatch(batch)
		}
	}
}

type apiResp struct {
	Status      string `json:"status"`
	Country     string `json:"country"`
	CountryCode string `json:"countryCode"`
	RegionName  string `json:"regionName"`
	City        string `json:"city"`
	ISP         string `json:"isp"`
	Org         string `json:"org"`
	AS          string `json:"as"`
	ASName      string `json:"asname"`
	Reverse     string `json:"reverse"`
	Mobile      bool   `json:"mobile"`
	Proxy       bool   `json:"proxy"`
	Hosting     bool   `json:"hosting"`
	Query       string `json:"query"`
}

const ipapiURL = "http://ip-api.com/batch?fields=status,country,countryCode,regionName,city,isp,org,as,asname,reverse,mobile,proxy,hosting,query"

func (g *GeoResolver) ipapiBatch(ips []string) (map[string]*Geo, error) {
	g.mu.Lock()
	until := g.limitedUntil
	g.mu.Unlock()
	if time.Now().Before(until) {
		return nil, fmt.Errorf("rate limited")
	}
	body, _ := json.Marshal(ips)
	req, _ := http.NewRequest("POST", ipapiURL, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := g.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	ttl, _ := strconv.Atoi(resp.Header.Get("X-Ttl"))
	if resp.StatusCode == 429 || resp.Header.Get("X-Rl") == "0" {
		if ttl <= 0 {
			ttl = 60
		}
		g.mu.Lock()
		g.limitedUntil = time.Now().Add(time.Duration(ttl+1) * time.Second)
		g.mu.Unlock()
		if resp.StatusCode == 429 {
			return nil, fmt.Errorf("429")
		}
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("http %d", resp.StatusCode)
	}
	var arr []apiResp
	if err := json.NewDecoder(resp.Body).Decode(&arr); err != nil {
		return nil, err
	}
	out := map[string]*Geo{}
	for _, r := range arr {
		if r.Status != "success" {
			continue
		}
		asn, rest := "", ""
		if f := strings.Fields(r.AS); len(f) > 0 {
			asn = f[0]
			rest = strings.Join(f[1:], " ")
		}
		org := r.Org
		if org == "" {
			org = rest
		}
		out[r.Query] = &Geo{IP: r.Query, ASN: asn, ASName: r.ASName, Org: org, ISP: r.ISP,
			Country: r.Country, CC: r.CountryCode, Region: r.RegionName, City: r.City,
			Reverse: r.Reverse, Mobile: r.Mobile, Proxy: r.Proxy, Hosting: r.Hosting, Source: "ip-api.com"}
	}
	return out, nil
}

// cymruLookup: DNS based fallback (Team Cymru IP-to-ASN mapping).
func cymruLookup(ip string) (*Geo, error) {
	v4 := net.ParseIP(ip).To4()
	if v4 == nil {
		return nil, fmt.Errorf("not ipv4")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	q := fmt.Sprintf("%d.%d.%d.%d.origin.asn.cymru.com", v4[3], v4[2], v4[1], v4[0])
	txt, err := net.DefaultResolver.LookupTXT(ctx, q)
	if err != nil || len(txt) == 0 {
		return nil, fmt.Errorf("no cymru data")
	}
	parts := strings.Split(txt[0], "|")
	asnFields := strings.Fields(parts[0])
	if len(asnFields) == 0 {
		return nil, fmt.Errorf("bad cymru data")
	}
	g := &Geo{IP: ip, ASN: "AS" + asnFields[0], Source: "cymru-dns"}
	if len(parts) > 2 {
		g.CC = strings.TrimSpace(parts[2])
	}
	if t2, err := net.DefaultResolver.LookupTXT(ctx, "AS"+asnFields[0]+".asn.cymru.com"); err == nil && len(t2) > 0 {
		p2 := strings.Split(t2[0], "|")
		name := strings.TrimSpace(p2[len(p2)-1])
		g.Org = name
		if i := strings.LastIndex(name, ","); i > 0 {
			g.ASName = strings.TrimSpace(name[:i])
		} else {
			g.ASName = name
		}
	}
	return g, nil
}

func (g *GeoResolver) resolveBatch(batch []string) {
	// ip-api (rich data) and Team Cymru DNS (fast ASN) run in parallel.
	// Cymru results are shown immediately, ip-api results upgrade them later.
	apiCh := make(chan map[string]*Geo, 1)
	go func() {
		res, err := g.ipapiBatch(batch)
		if err != nil {
			g.mu.Lock()
			if time.Now().After(g.limitedUntil) { // blocked / unreachable: skip ip-api for a while
				g.limitedUntil = time.Now().Add(60 * time.Second)
			}
			g.mu.Unlock()
			res = nil
		}
		apiCh <- res
	}()

	var wg sync.WaitGroup
	for _, ip := range batch {
		wg.Add(1)
		go func(ip string) {
			defer wg.Done()
			c, err := cymruLookup(ip)
			if err != nil {
				return
			}
			g.mu.Lock()
			if g.cache[ip] == nil {
				g.cache[ip] = c
			}
			g.mu.Unlock()
		}(ip)
	}
	wg.Wait()
	api := <-apiCh

	g.mu.Lock()
	for _, ip := range batch {
		if a := api[ip]; a != nil {
			if old := g.cache[ip]; old != nil && a.Reverse == "" {
				a.Reverse = old.Reverse
			}
			g.cache[ip] = a
		} else if g.cache[ip] == nil {
			g.cache[ip] = &Geo{IP: ip, Source: "none"}
		}
		delete(g.queued, ip)
		g.pending--
	}
	g.mu.Unlock()
	for _, ip := range batch {
		if v := g.Get(ip); v != nil && v.Reverse == "" {
			go g.rdnsFill(ip)
		}
	}
}
