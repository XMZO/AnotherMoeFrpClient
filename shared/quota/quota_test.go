package quota

import "testing"

func TestParsePorts(t *testing.T) {
	r, err := ParsePorts(" 30000, 10000-20000 ")
	if err != nil || r.String() != "10000-20000,30000" {
		t.Fatalf("%v %v", r, err)
	}
	for p, want := range map[int]bool{9999: false, 10000: true, 20000: true, 25000: false, 30000: true} {
		if r.Contains(p) != want {
			t.Errorf("Contains(%d) = %v", p, !want)
		}
	}
	if r, _ := ParsePorts(""); !r.Contains(1) {
		t.Error("空范围不限")
	}
	for _, bad := range []string{"0", "70000", "20000-10000", "abc", "1-2-3"} {
		if _, err := ParsePorts(bad); err == nil {
			t.Errorf("%q 应报错", bad)
		}
	}
}

func TestApplyAndValidate(t *testing.T) {
	base := Quota{MaxProxies: 5, Ports: "10000-20000", Types: []string{"tcp"}}
	n, p := 50, ""
	got := base.Apply(&Override{MaxProxies: &n, Ports: &p})
	if got.MaxProxies != 50 || got.Ports != "" || len(got.Types) != 1 {
		t.Fatalf("%+v", got)
	}
	if !got.AllowsType("tcp") || got.AllowsType("http") || !(Quota{}).AllowsType("http") {
		t.Fatal("AllowsType")
	}
	if (Quota{Types: []string{"nope"}}).Validate() == nil || (Quota{MaxProxies: -1}).Validate() == nil {
		t.Fatal("非法限额应报错")
	}
	if DefaultRoles().Validate() != nil || DefaultRoles().For("admin").MaxProxies != 0 || DefaultRoles().For("user").MaxProxies != 5 || DefaultRoles().For("vip").MaxProxies != 5 {
		t.Fatal("默认值")
	}
	if !(&Override{}).Empty() || (&Override{Ports: &p}).Empty() {
		t.Fatal("Empty")
	}
}
