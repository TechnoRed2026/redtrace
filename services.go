package main

import (
	"encoding/binary"
	"net"
	"strconv"
	"strings"
	"time"
)

// Service is a preset (game / app / infrastructure) with its default port,
// transport protocol and – for UDP – an optional query payload so that real
// servers actually answer the probe.
type Service struct {
	Name    string
	Group   string
	Port    int
	Proto   Proto
	Alias   string
	Payload func(dst net.IP, port int) []byte
}

var genericPayload = []byte("RedTrace probe 0123456789abcdef")

func a2sInfo(net.IP, int) []byte {
	return append([]byte{0xFF, 0xFF, 0xFF, 0xFF, 0x54}, []byte("Source Engine Query\x00")...)
}

func raknetPing(net.IP, int) []byte {
	b := []byte{0x01}
	t := make([]byte, 8)
	binary.BigEndian.PutUint64(t, uint64(time.Now().UnixMilli()))
	b = append(b, t...)
	b = append(b, 0x00, 0xff, 0xff, 0x00, 0xfe, 0xfe, 0xfe, 0xfe, 0xfd, 0xfd, 0xfd, 0xfd, 0x12, 0x34, 0x56, 0x78)
	b = append(b, 0, 0, 0, 0, 0, 0, 0, 0x2a)
	return b
}

func sampQuery(dst net.IP, port int) []byte {
	ip := dst.To4()
	if ip == nil {
		return genericPayload
	}
	b := []byte("SAMP")
	return append(b, ip[0], ip[1], ip[2], ip[3], byte(port&0xff), byte(port>>8), 'i')
}

func dnsQuery(net.IP, int) []byte {
	b := []byte{0x12, 0x34, 0x01, 0x00, 0, 1, 0, 0, 0, 0, 0, 0}
	for _, l := range []string{"example", "com"} {
		b = append(b, byte(len(l)))
		b = append(b, l...)
	}
	return append(b, 0, 0, 1, 0, 1)
}

func ntpQuery(net.IP, int) []byte {
	b := make([]byte, 48)
	b[0] = 0x1b
	return b
}

func mumblePing(net.IP, int) []byte {
	return []byte{0, 0, 0, 0, 1, 2, 3, 4, 5, 6, 7, 8}
}

func svc(name, group string, proto Proto, port int, alias string, pl func(net.IP, int) []byte) *Service {
	return &Service{Name: name, Group: group, Proto: proto, Port: port, Alias: alias, Payload: pl}
}

var services = []*Service{
	// ---- Minecraft ----
	svc("Minecraft Java Edition", "Minecraft", ProtoTCP, 25565, "mc minecraft java hypixel", nil),
	svc("Minecraft Bedrock / Pocket", "Minecraft", ProtoUDP, 19132, "mc minecraft bedrock mcpe pocket realms raknet", raknetPing),
	// ---- Counter-Strike & Valve ----
	svc("Counter-Strike 2 / CS:GO (game)", "Valve", ProtoUDP, 27015, "cs cs2 csgo counter strike source steam", a2sInfo),
	svc("Counter-Strike 2 / CS:GO (RCON)", "Valve", ProtoTCP, 27015, "cs cs2 csgo rcon counter strike", nil),
	svc("Counter-Strike 1.6 / Source / CZ", "Valve", ProtoUDP, 27015, "cs cs16 1.6 css condition zero hlds goldsrc", a2sInfo),
	svc("Steam client / matchmaking", "Valve", ProtoUDP, 27020, "steam valve sdr", nil),
	svc("Dota 2", "Valve", ProtoUDP, 27015, "dota dota2 valve", a2sInfo),
	svc("Team Fortress 2", "Valve", ProtoUDP, 27015, "tf2 valve", a2sInfo),
	svc("Left 4 Dead 2", "Valve", ProtoUDP, 27015, "l4d2 l4d valve", a2sInfo),
	svc("Garry's Mod", "Valve", ProtoUDP, 27015, "gmod garrys valve", a2sInfo),
	// ---- GTA / Rockstar ----
	svc("GTA V - FiveM (TCP)", "GTA", ProtoTCP, 30120, "gta gta5 gtav fivem cfx roleplay rp redm", nil),
	svc("GTA V - FiveM (UDP)", "GTA", ProtoUDP, 30120, "gta gta5 gtav fivem cfx roleplay rp redm", nil),
	svc("GTA V Online (Rockstar)", "GTA", ProtoUDP, 6672, "gta gta5 gtav online rockstar", nil),
	svc("GTA San Andreas - SA-MP", "GTA", ProtoUDP, 7777, "gta samp sa-mp san andreas", sampQuery),
	svc("GTA SA - MTA:SA (game)", "GTA", ProtoUDP, 22003, "gta mta mtasa san andreas", nil),
	svc("GTA SA - MTA:SA (HTTP)", "GTA", ProtoTCP, 22005, "gta mta mtasa san andreas", nil),
	svc("GTA V - RAGE:MP", "GTA", ProtoUDP, 22005, "gta ragemp rage", nil),
	svc("GTA V - alt:V", "GTA", ProtoUDP, 7788, "gta altv alt:v", nil),
	// ---- Voice ----
	svc("TeamSpeak 3 - Voice", "Voice", ProtoUDP, 9987, "ts ts3 teamspeak voice", nil),
	svc("TeamSpeak 3 - File transfer", "Voice", ProtoTCP, 30033, "ts ts3 teamspeak file", nil),
	svc("TeamSpeak 3 - ServerQuery", "Voice", ProtoTCP, 10011, "ts ts3 teamspeak query", nil),
	svc("Mumble", "Voice", ProtoUDP, 64738, "mumble voice murmur", mumblePing),
	svc("Discord Voice (UDP range)", "Voice", ProtoUDP, 50001, "discord voice rtc", nil),
	svc("Discord / HTTPS gateway", "Voice", ProtoTCP, 443, "discord gateway", nil),
	// ---- Other games ----
	svc("Valorant / Riot game servers", "Riot", ProtoUDP, 7000, "valorant riot", nil),
	svc("League of Legends (game)", "Riot", ProtoUDP, 5000, "lol league riot", nil),
	svc("League of Legends (chat)", "Riot", ProtoTCP, 5223, "lol league riot chat", nil),
	svc("Fortnite", "Epic", ProtoUDP, 9000, "fortnite epic", nil),
	svc("Rocket League", "Epic", ProtoUDP, 7000, "rocket league epic", nil),
	svc("PUBG", "Games", ProtoUDP, 7080, "pubg battlegrounds", nil),
	svc("Apex Legends", "Games", ProtoUDP, 37000, "apex legends ea respawn", nil),
	svc("Call of Duty / Warzone / Xbox Live", "Games", ProtoUDP, 3074, "cod warzone call duty xbox live", nil),
	svc("Rainbow Six Siege", "Games", ProtoUDP, 6015, "r6 siege rainbow ubisoft", nil),
	svc("Rust (game)", "Games", ProtoUDP, 28015, "rust facepunch", a2sInfo),
	svc("Rust (RCON)", "Games", ProtoTCP, 28016, "rust rcon", nil),
	svc("ARK: Survival (game)", "Games", ProtoUDP, 7777, "ark survival evolved ascended", nil),
	svc("ARK: Survival (query)", "Games", ProtoUDP, 27015, "ark query", a2sInfo),
	svc("Terraria", "Games", ProtoTCP, 7777, "terraria", nil),
	svc("Valheim", "Games", ProtoUDP, 2456, "valheim", nil),
	svc("Palworld", "Games", ProtoUDP, 8211, "palworld pal", nil),
	svc("7 Days to Die", "Games", ProtoUDP, 26900, "7d2d seven days die", nil),
	svc("Roblox", "Games", ProtoUDP, 49152, "roblox", nil),
	svc("World of Warcraft / Battle.net", "Blizzard", ProtoTCP, 3724, "wow warcraft blizzard battlenet", nil),
	svc("Battle.net client", "Blizzard", ProtoTCP, 1119, "battlenet blizzard overwatch diablo", nil),
	svc("PlayStation Network / STUN", "Console", ProtoUDP, 3478, "psn playstation ps4 ps5 stun", nil),
	// ---- Infrastructure ----
	svc("ICMP Ping (classic traceroute)", "Network", ProtoICMP, 0, "icmp ping echo", nil),
	svc("HTTPS", "Network", ProtoTCP, 443, "https web tls ssl", nil),
	svc("HTTP", "Network", ProtoTCP, 80, "http web", nil),
	svc("QUIC / HTTP3", "Network", ProtoUDP, 443, "quic http3 udp443", nil),
	svc("DNS (UDP)", "Network", ProtoUDP, 53, "dns resolver", dnsQuery),
	svc("DNS (TCP)", "Network", ProtoTCP, 53, "dns resolver", nil),
	svc("NTP", "Network", ProtoUDP, 123, "ntp time", ntpQuery),
	svc("SSH", "Network", ProtoTCP, 22, "ssh", nil),
	svc("RDP", "Network", ProtoTCP, 3389, "rdp remote desktop", nil),
	svc("FTP", "Network", ProtoTCP, 21, "ftp", nil),
	svc("SMTP", "Network", ProtoTCP, 25, "smtp mail", nil),
	svc("OpenVPN (UDP)", "VPN", ProtoUDP, 1194, "openvpn vpn", nil),
	svc("OpenVPN (TCP)", "VPN", ProtoTCP, 443, "openvpn vpn", nil),
	svc("WireGuard", "VPN", ProtoUDP, 51820, "wireguard wg vpn", nil),
	svc("MySQL / MariaDB", "Database", ProtoTCP, 3306, "mysql mariadb db", nil),
	svc("PostgreSQL", "Database", ProtoTCP, 5432, "postgres postgresql db", nil),
	svc("Redis", "Database", ProtoTCP, 6379, "redis db", nil),
	svc("MongoDB", "Database", ProtoTCP, 27017, "mongo mongodb db", nil),
}

func serviceText(s *Service) string {
	p := strconv.Itoa(s.Port)
	return strings.ToLower(s.Name + " " + s.Group + " " + s.Alias + " " + s.Proto.String() + " " + p)
}

// filterServices returns services where every whitespace separated token of q
// appears in name / group / alias / protocol / port.
func filterServices(q string) []*Service {
	toks := strings.Fields(strings.ToLower(q))
	if len(toks) == 0 {
		return services
	}
	var out []*Service
	for _, s := range services {
		t := serviceText(s)
		ok := true
		for _, tk := range toks {
			if !strings.Contains(t, tk) {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, s)
		}
	}
	return out
}

// FindService resolves a --service argument to the first sensible match.
func FindService(q string) *Service {
	q = strings.ToLower(strings.TrimSpace(q))
	for _, s := range services { // exact alias token
		for _, a := range strings.Fields(s.Alias) {
			if a == q {
				return s
			}
		}
	}
	if l := filterServices(q); len(l) > 0 && q != "" {
		return l[0]
	}
	return nil
}

func ServiceByPort(p Proto, port int) *Service {
	for _, s := range services {
		if s.Proto == p && s.Port == port && s.Payload != nil {
			return s
		}
	}
	return nil
}

func payloadFor(cfg Config, dest net.IP) []byte {
	s := cfg.Service
	if s == nil {
		s = ServiceByPort(cfg.Proto, cfg.Port)
	}
	if s != nil && s.Payload != nil {
		return s.Payload(dest, cfg.Port)
	}
	return genericPayload
}
