package main

import "testing"

func TestParseRunAs(t *testing.T) {
	env := func(kv map[string]string) func(string) (string, bool) {
		return func(k string) (string, bool) { v, ok := kv[k]; return v, ok }
	}
	if _, ok, err := parseRunAs(env(nil)); ok || err != nil {
		t.Fatal("没设置 PUID 时不降权")
	}
	if r, ok, err := parseRunAs(env(map[string]string{"MOEFRP_PUID": "1000"})); !ok || err != nil || r != (runAs{1000, 1000}) {
		t.Fatalf("PGID 默认等于 PUID：%v %v %v", r, ok, err)
	}
	if r, _, _ := parseRunAs(env(map[string]string{"MOEFRP_PUID": "65532", "MOEFRP_PGID": " 100 "})); r != (runAs{65532, 100}) {
		t.Fatalf("%v", r)
	}
	for _, bad := range []map[string]string{{"MOEFRP_PUID": "abc"}, {"MOEFRP_PUID": "-1"}, {"MOEFRP_PUID": "1", "MOEFRP_PGID": "x"}} {
		if _, _, err := parseRunAs(env(bad)); err == nil {
			t.Fatalf("应报错：%v", bad)
		}
	}
}

func TestInitialUserNamespace(t *testing.T) {
	if !initialUserNamespace("         0          0 4294967295\n") {
		t.Fatal("宿主机原始映射")
	}
	if initialUserNamespace("         0       1000          1\n         1     100000      65536\n") {
		t.Fatal("rootless 容器不是原始映射")
	}
	if initialUserNamespace("") {
		t.Fatal("空内容")
	}
}
