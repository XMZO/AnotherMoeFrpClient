package nodes

import (
	"testing"

	"moefrp/shared/frpsset"
)

func TestFromTOML(t *testing.T) {
	got := fromTOML(`# 旧配置
bindPort = 7100
auth.token = "secret"
kcpBindPort = 7100
vhostHTTPPort = 8080
subDomainHost = "frp.example.com"
maxPortsPerClient = 5
allowPorts = [{ start = 20000, end = 20100 }, { single = 30000 }]
log.level = "warn"
transport.tcpMux = false
transport.tls.force = true
`)
	want := frpsset.Defaults()
	want.BindPort, want.KCPBindPort, want.VhostHTTPPort, want.SubDomainHost = 7100, 7100, 8080, "frp.example.com"
	want.MaxPortsPerClient, want.AllowPorts, want.LogLevel = 5, "20000-20100,30000", "warn"
	want.TCPMux, want.TLSForce = false, true
	if got != want {
		t.Fatalf("转换结果：\n%+v\n%+v", got, want)
	}
	if err := got.Validate(); err != nil {
		t.Fatal(err)
	}
	// 空的、解析不了的：默认值
	if fromTOML("") != frpsset.Defaults() || fromTOML("bindPort = 'x'") != frpsset.Defaults() {
		t.Fatal("应回到默认值")
	}
}
