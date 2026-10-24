package frpsset

import (
	"errors"
	"testing"
)

func TestSchema(t *testing.T) {
	fs := Schema()
	groups := map[string]bool{}
	for _, g := range Groups {
		groups[g.Key] = true
	}
	seen := map[string]bool{}
	for _, f := range fs {
		if !groups[f.Group] || f.Label == "" || seen[f.Key] {
			t.Fatalf("字段 %+v", f)
		}
		seen[f.Key] = true
	}
	if !seen["bindPort"] || !seen["subDomainHost"] || len(fs) < 15 {
		t.Fatalf("schema: %d", len(fs))
	}
	for _, f := range fs {
		if f.Key == "logLevel" && (f.Kind != "select" || len(f.Options) != 5) {
			t.Fatalf("logLevel: %+v", f)
		}
		if f.Key == "bindPort" && (f.Min == nil || *f.Min != 1 || *f.Max != 65535) {
			t.Fatalf("bindPort: %+v", f)
		}
	}
}

func TestValidate(t *testing.T) {
	if err := Defaults().Validate(); err != nil {
		t.Fatal(err)
	}
	bad := map[string]func(*Settings){
		"bindPort":              func(s *Settings) { s.BindPort = 0 },
		"logLevel":              func(s *Settings) { s.LogLevel = "loud" },
		"bindAddr":              func(s *Settings) { s.BindAddr = "localhost" },
		"allowPorts":            func(s *Settings) { s.AllowPorts = "70000" },
		"vhostHTTPPort":         func(s *Settings) { s.VhostHTTPPort = 7000 },
		"tcpmuxHTTPConnectPort": func(s *Settings) { s.VhostHTTPSPort = 443; s.TCPMuxHTTPConnectPort = 443 },
		"quicBindPort":          func(s *Settings) { s.KCPBindPort = 7001; s.QUICBindPort = 7001 },
		"subDomainHost":         func(s *Settings) { s.SubDomainHost = "frp.example.com" },
	}
	for key, edit := range bad {
		s := Defaults()
		edit(&s)
		var fe *FieldError
		if err := s.Validate(); !errors.As(err, &fe) || fe.Key != key {
			t.Fatalf("%s：%v", key, err)
		}
	}
	ok := Defaults()
	ok.KCPBindPort, ok.QUICBindPort = 7000, 7002 // UDP 可以和客户端连接端口相同
	ok.VhostHTTPPort, ok.SubDomainHost = 80, "frp.example.com"
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
}
