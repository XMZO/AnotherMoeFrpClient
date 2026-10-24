package sysinfo

import (
	"os"
	"testing"
	"time"
)

func TestParseEchoIP(t *testing.T) {
	for body, want := range map[string]string{
		`{"ip":"203.0.113.7","country":"JP"}`:                "203.0.113.7",
		"fl=1\nh=www.cloudflare.com\nip=2001:db8::5\nts=1\n": "2001:db8::5",
		"198.51.100.9\n":         "198.51.100.9",
		"<html>not an ip</html>": "",
	} {
		got := parseEchoIP(body)
		if (got == nil && want != "") || (got != nil && got.String() != want) {
			t.Errorf("%q → %v，期望 %q", body, got, want)
		}
	}
}

// 手动跑：MOEFRP_PROBE=1 go test -run TestProbeLive -v ./internal/sysinfo（真的去问外面的服务）
func TestProbeLive(t *testing.T) {
	if os.Getenv("MOEFRP_PROBE") == "" {
		t.Skip("set MOEFRP_PROBE=1")
	}
	urls := append([]string{os.Getenv("MOEFRP_PROBE_PANEL")}, ipEchoURLs...)
	for _, network := range []string{"tcp4", "tcp6"} {
		for _, u := range urls {
			if u == "" {
				continue
			}
			start := time.Now()
			ip := askEcho(t.Context(), probeClient(network), u, network == "tcp4")
			t.Logf("%s %-45s %-40q %v", network, u, ip, time.Since(start).Round(time.Millisecond))
		}
	}
	v4, v6 := ProbePublicIPs(t.Context(), os.Getenv("MOEFRP_PROBE_PANEL"))
	t.Logf("result v4=%q v6=%q", v4, v6)
}
