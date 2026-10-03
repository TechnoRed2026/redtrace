# RedTrace

Traceroute with a TUI for games and services. Shows per hop: IP, ASN, ASN name, company/ISP, location,
connection type (ISP / Datacenter / Mobile / VPN / LAN / CGNAT), the proto:port the probes used, loss and RTT.

## Install (Windows)
1. Install Go (https://go.dev/dl/ or `winget install GoLang.Go`).
2. Double-click `install.bat` -> builds `redtrace.exe`, copies it to `%LOCALAPPDATA%\RedTrace`, adds it to your user PATH.
3. Open a NEW terminal and run `redtrace`.

Raw ICMP sockets need Administrator: RedTrace asks for it by itself (UAC prompt) and opens in an elevated window.
Remove with `uninstall.bat`. A prebuilt `redtrace.exe` (amd64) is included if you prefer not to build.

## Use
    redtrace                              interactive TUI
    redtrace mc.hypixel.net -s minecraft  trace with the Minecraft Java preset (TCP 25565)
    redtrace 1.2.3.4 -s cs2               CS2 / CS:GO (UDP 27015, real A2S query payload)
    redtrace 1.2.3.4 -P udp -p 9987       custom proto/port (TeamSpeak voice)
    redtrace 1.1.1.1 -n                   plain text report, no TUI
    redtrace --list                       all presets

TUI: Tab switches panels | type to search services (name, game, port, "cs", "gta", "ts"...) | Enter starts |
c copies the report to clipboard | s saves a .txt | r reruns | e edits settings | q quits.

Probe modes: ICMP echo, UDP (with real query payloads for Source/CS, Minecraft Bedrock, SA-MP, DNS, NTP, Mumble),
TCP SYN (connect with a limited TTL; SYN-ACK = open, RST = closed, both mean the host was reached).

ASN/company data: ip-api.com (free, non-commercial, rate limited) with automatic fallback to Team Cymru DNS.
No third-party Go modules - standard library only.
