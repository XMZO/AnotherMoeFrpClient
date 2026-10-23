package credstore

import (
	"crypto/rand"
	"encoding/hex"
	"testing"
)

// 真实读写凭据管理器，用随机目标名，测试结束删除。
func TestWinCredRoundTrip(t *testing.T) {
	var b [6]byte
	_, _ = rand.Read(b[:])
	w := NewWinCred("MoeFrp/unittest-" + hex.EncodeToString(b[:]))
	t.Cleanup(func() { _ = w.Delete() })

	if _, ok, err := w.Load(); ok || err != nil {
		t.Fatalf("fresh target: ok=%v err=%v", ok, err)
	}
	if err := w.Save(Cred{Nickname: "alice", Token: ""}); err != ErrEmptyToken {
		t.Fatalf("empty token: %v", err)
	}
	if err := w.Save(Cred{Nickname: "alice", Token: "mfs_abc"}); err != nil {
		t.Fatal(err)
	}
	c, ok, err := w.Load()
	if err != nil || !ok || c.Token != "mfs_abc" || c.Nickname != "alice" {
		t.Fatalf("load: %+v %v %v", c, ok, err)
	}
	if err := w.Save(Cred{Nickname: "bob", Token: "mfs_def"}); err != nil {
		t.Fatal(err)
	}
	if c, _, _ := w.Load(); c.Token != "mfs_def" || c.Nickname != "bob" {
		t.Fatalf("overwrite: %+v", c)
	}
	if err := w.Delete(); err != nil {
		t.Fatal(err)
	}
	if err := w.Delete(); err != nil {
		t.Fatalf("second delete should be a no-op: %v", err)
	}
	if _, ok, _ := w.Load(); ok {
		t.Fatal("deleted credential still readable")
	}
}
