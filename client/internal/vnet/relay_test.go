package vnet

import (
	"slices"
	"testing"
)

// 报给面板的收件中继：只报真正连着的；主的没连上、备用连着时，给旧版本的 relay 也指向备用
func TestHomesLocked(t *testing.T) {
	e := &Engine{}
	e.rs.home, e.rs.backup = "a", "b"
	e.rs.conns = map[string]*relayConn{"a": {}, "b": {}}
	if relay, homes := e.homesLocked(); relay != "a" || !slices.Equal(homes, []string{"a", "b"}) {
		t.Fatalf("都连着：%q %v", relay, homes)
	}
	delete(e.rs.conns, "a")
	if relay, homes := e.homesLocked(); relay != "b" || !slices.Equal(homes, []string{"b"}) {
		t.Fatalf("主的断了：%q %v", relay, homes)
	}
	e.rs.conns = map[string]*relayConn{}
	if relay, homes := e.homesLocked(); relay != "a" || len(homes) != 0 {
		t.Fatalf("都没连上：%q %v", relay, homes)
	}
}

// 发给某人的中继包：从他连着的节点里挑我也连着、我这边延迟最低的；旧版本只有 relay
func TestRelayForLocked(t *testing.T) {
	e := &Engine{}
	e.rs.conns = map[string]*relayConn{"a": {}, "b": {}}
	e.rs.latency = map[string]int{"a": 80, "b": 30, "c": 10}
	if got := e.relayForLocked(&peer{relay: "a", homes: []string{"a", "b"}}); got != "b" {
		t.Fatalf("应挑延迟低的 b，得到 %q", got)
	}
	if got := e.relayForLocked(&peer{relay: "a", homes: []string{"a", "c"}}); got != "a" {
		t.Fatalf("c 我没连着，应挑 a，得到 %q", got)
	}
	if got := e.relayForLocked(&peer{relay: "a"}); got != "a" {
		t.Fatalf("旧版本只有 relay：得到 %q", got)
	}
	if got := e.relayForLocked(&peer{homes: []string{"c"}}); got != "" {
		t.Fatalf("一个都没连着应走面板，得到 %q", got)
	}
}
