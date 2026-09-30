package common

import (
	"net"
	"os"
	"runtime"
	"sort"
	"strings"
	"testing"
)

func TestCollectHostInfo(t *testing.T) {
	hi := CollectHostInfo()
	if hi.OS != runtime.GOOS || hi.Arch != runtime.GOARCH {
		t.Fatalf("platform = %q/%q, want %q/%q", hi.OS, hi.Arch, runtime.GOOS, runtime.GOARCH)
	}
	if name, err := os.Hostname(); err == nil && hi.Hostname != name {
		t.Fatalf("hostname = %q, want %q", hi.Hostname, name)
	}
	if !sort.StringsAreSorted(hi.IPs) {
		t.Fatalf("ips not sorted: %v", hi.IPs)
	}
	seen := make(map[string]struct{}, len(hi.IPs))
	for _, s := range hi.IPs {
		ip := net.ParseIP(s)
		if ip == nil {
			t.Fatalf("ip %q does not parse", s)
		}
		if ip.IsLoopback() {
			t.Fatalf("ip %q is loopback (must be skipped)", s)
		}
		if _, dup := seen[s]; dup {
			t.Fatalf("ip %q duplicated", s)
		}
		seen[s] = struct{}{}
	}
	if len(hi.IPs) > maxHostIPs {
		t.Fatalf("ips = %d entries, want at most %d", len(hi.IPs), maxHostIPs)
	}
}

func TestHostInfoSummary(t *testing.T) {
	if got := (HostInfo{OS: "linux", Arch: "amd64", Hostname: "edge-1"}).Summary(); got != "linux/amd64 @edge-1" {
		t.Fatalf("summary = %q", got)
	}
	if got := (HostInfo{OS: "darwin"}).Summary(); got != "darwin" {
		t.Fatalf("summary = %q", got)
	}
	if got := (HostInfo{}).Summary(); got != "" {
		t.Fatalf("summary = %q, want empty", got)
	}
	if !strings.Contains((HostInfo{OS: "linux", Arch: "arm64"}).Summary(), "linux/arm64") {
		t.Fatalf("summary missing platform")
	}
}
