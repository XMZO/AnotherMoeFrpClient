package nodeticket

import (
	"strings"
	"testing"
)

func TestSignVerify(t *testing.T) {
	master := []byte("0123456789abcdef0123456789abcdef")
	k1, k2 := NodeKey(master, "node-1"), NodeKey(master, "node-2")
	if string(k1) == string(k2) {
		t.Fatal("不同节点的密钥应该不同")
	}
	c := Claims{User: "u1", Session: "s1", Node: "node-1", Gen: 2, Issued: 100, Expires: 1000, Nonce: NewNonce()}
	tk := Sign(k1, c)
	if !strings.HasPrefix(tk, Prefix) {
		t.Fatal(tk)
	}
	got, err := Verify(k1, tk)
	if err != nil || got != c {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := Verify(k2, tk); err == nil {
		t.Fatal("别的节点的密钥不能通过校验")
	}
	// 改动声明：签名对不上
	p, s, _ := strings.Cut(strings.TrimPrefix(tk, Prefix), ".")
	other := Sign(k1, Claims{User: "u2", Session: "s1", Node: "node-1", Expires: 1000, Nonce: "x"})
	p2, _, _ := strings.Cut(strings.TrimPrefix(other, Prefix), ".")
	for _, bad := range []string{"", "mfc_abc", Prefix + p, Prefix + p2 + "." + s, Prefix + p + ".!!", tk + "x"} {
		if _, err := Verify(k1, bad); err == nil {
			t.Fatalf("应该拒绝 %q", bad)
		}
	}
	if NewNonce() == NewNonce() {
		t.Fatal("随机数重复")
	}
}
