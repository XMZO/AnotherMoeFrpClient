package nodes

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"moefrp/server/internal/store"
)

func newSecretService(t *testing.T, kek []byte) (*Service, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "moefrp.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if _, err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return New(Options{Store: st, KEK: kek, KEKSource: "test"}), st
}

// 主密钥在库里加密保存；旧版明文自动改成加密；KEK 不对时解不开、报清楚的原因；指纹是分组格式。
func TestSealedMasterKey(t *testing.T) {
	ctx := context.Background()
	kek := make([]byte, 32)
	kek[0] = 1
	s, st := newSecretService(t, kek)
	key, err := s.credKey(ctx)
	if err != nil {
		t.Fatal(err)
	}
	v, _, _ := store.GetSetting(ctx, st.DB(), settingCredKey)
	if !strings.HasPrefix(v, sealedPrefix) || strings.Contains(v, base64.StdEncoding.EncodeToString(key)) {
		t.Fatalf("库里应是密文：%q", v)
	}
	info, _ := s.Secret(ctx)
	if !regexp.MustCompile(`^[A-Z2-7]{4}(-[A-Z2-7]{4}){3}$`).MatchString(info.Fingerprint) || info.Version != 1 {
		t.Fatalf("指纹：%+v", info)
	}

	// 轮换：指纹变了、版本加一、带上之前的指纹
	r, err := s.RotateSecret(ctx, Actor{})
	if err != nil || r.Fingerprint == info.Fingerprint || r.Previous != info.Fingerprint || r.Version != 2 {
		t.Fatalf("轮换：%+v %v", r, err)
	}

	// 旧版明文：读到后改成加密保存，密钥不变
	legacy := make([]byte, 32)
	legacy[5] = 9
	store.SetSetting(ctx, st.DB(), settingCredKey, base64.StdEncoding.EncodeToString(legacy))
	s2 := New(Options{Store: st, KEK: kek, KEKSource: "test"})
	got, err := s2.credKey(ctx)
	if err != nil || string(got) != string(legacy) {
		t.Fatalf("旧版明文：%v", err)
	}
	if v, _, _ := store.GetSetting(ctx, st.DB(), settingCredKey); !strings.HasPrefix(v, sealedPrefix) {
		t.Fatal("旧版明文应改成加密保存")
	}

	// KEK 换了：解不开，后台显示原因，不偷偷生成新密钥
	other := make([]byte, 32)
	s3 := New(Options{Store: st, KEK: other, KEKSource: "test"})
	if _, err := s3.credKey(ctx); !errors.Is(err, ErrKeyUnreadable) {
		t.Fatalf("应解不开：%v", err)
	}
	if info, _ := s3.Secret(ctx); info.Error == "" || info.Fingerprint != "" {
		t.Fatalf("应显示原因：%+v", info)
	}
}

func TestLoadKEK(t *testing.T) {
	file := filepath.Join(t.TempDir(), "sub", "secret.key")
	k1, src, err := LoadKEK("", file)
	if err != nil || len(k1) != 32 || src != file {
		t.Fatal(err)
	}
	if st, _ := os.Stat(file); st == nil {
		t.Fatal("应生成文件")
	}
	k2, _, _ := LoadKEK("", file)
	if string(k1) != string(k2) {
		t.Fatal("第二次应读到同一个")
	}
	inline := base64.StdEncoding.EncodeToString(make([]byte, 32))
	if k, src, err := LoadKEK(inline, file); err != nil || len(k) != 32 || src != "MOEFRP_SECRET_KEY" {
		t.Fatalf("环境变量优先：%v", err)
	}
	if _, _, err := LoadKEK("short", file); err == nil {
		t.Fatal("格式不对应报错")
	}
}
