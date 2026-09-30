package common

import (
	"net"
	"os"
	"runtime"
	"sort"
)

// HostInfo describes the machine a client runs on. Clients collect it
// locally and advertise it in every heartbeat so operators can tell
// clients apart (OS/arch, hostname, addresses). It is self-reported
// telemetry, shown to tenant viewers — never used for auth or routing.
type HostInfo struct {
	OS       string   `json:"os,omitempty"`
	Arch     string   `json:"arch,omitempty"`
	Hostname string   `json:"hostname,omitempty"`
	IPs      []string `json:"ips,omitempty"`
}

// Empty reports whether no host information is known.
func (h HostInfo) Empty() bool {
	return h.OS == "" && h.Arch == "" && h.Hostname == "" && len(h.IPs) == 0
}

// Summary renders a compact one-line label (e.g. "linux/amd64 @edge-1").
func (h HostInfo) Summary() string {
	platform := h.OS
	if h.Arch != "" {
		if platform != "" {
			platform += "/"
		}
		platform += h.Arch
	}
	if h.Hostname != "" {
		if platform != "" {
			platform += " "
		}
		platform += "@" + h.Hostname
	}
	return platform
}

// maxHostIPs bounds the advertised address list so one multi-homed host
// cannot bloat every heartbeat.
const maxHostIPs = 64

// CollectHostInfo gathers the local OS/arch, hostname, and unicast IP
// addresses (both v4 and v6). Only interfaces that are up are considered,
// loopback addresses are skipped, and the result is deduplicated and
// sorted for stable output.
func CollectHostInfo() HostInfo {
	out := HostInfo{OS: runtime.GOOS, Arch: runtime.GOARCH}
	if name, err := os.Hostname(); err == nil {
		out.Hostname = name
	}
	seen := make(map[string]struct{})
	add := func(ip net.IP) {
		if len(seen) >= maxHostIPs {
			return
		}
		if ip == nil || ip.IsLoopback() {
			return
		}
		if v4 := ip.To4(); v4 != nil {
			ip = v4
		}
		s := ip.String()
		if s == "" || s == "<nil>" {
			return
		}
		if _, dup := seen[s]; !dup {
			seen[s] = struct{}{}
			out.IPs = append(out.IPs, s)
		}
	}
	ifaces, err := net.Interfaces()
	if err == nil {
		for _, iface := range ifaces {
			if iface.Flags&net.FlagUp == 0 {
				continue
			}
			addrs, err := iface.Addrs()
			if err != nil {
				continue
			}
			for _, addr := range addrs {
				switch v := addr.(type) {
				case *net.IPNet:
					add(v.IP)
				case *net.IPAddr:
					add(v.IP)
				}
			}
		}
	} else if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, addr := range addrs {
			switch v := addr.(type) {
			case *net.IPNet:
				add(v.IP)
			case *net.IPAddr:
				add(v.IP)
			}
		}
	}
	sort.Strings(out.IPs)
	return out
}
