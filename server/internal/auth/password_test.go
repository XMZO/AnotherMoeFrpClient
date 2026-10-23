package auth

import (
	"strings"
	"testing"
	"time"
)

// 以下哈希由 Python argon2-cffi 25.1.0 现场生成（脚本见 testdata/gen_legacy_hashes.py），
// 参数与旧 server.py 一致：time_cost=3, memory_cost=65536, parallelism=2, hash_len=32, salt_len=16。
// 密码是专门的测试密码，不来自旧 users.db。
const (
	legacyPassword     = "legacy-Passw0rd!"
	legacyHash         = "$argon2id$v=19$m=65536,t=3,p=2$8CXQOgjZR2VGrLN7x7Xsgg$VDdwfb4n404r6/bg+pjNpjPKmtW/1nz8BSvjWHY5UC4"
	legacyUTF8Password = "旧版密码-测试"
	legacyUTF8Hash     = "$argon2id$v=19$m=65536,t=3,p=2$5VkZOsQ2g5/NIlDun9+ekw$GN8uaiOznRnwSdLBFutYi/6ib0gtvRaoTRFiVAQYk2A"
	// argon2-cffi 25.x 的默认参数（p=4），与当前参数不同，登录后应重新哈希
	cffiDefaultHash = "$argon2id$v=19$m=65536,t=3,p=4$SGTcDaPZMjXr8nckvE5lvg$pkCdHZzDAzVwB2a7VgaHduXi/M8wHu36dnUywQnmrEg"
)

// testParams 是测试用的轻量参数，避免每次哈希都占 64MB、耗时上百毫秒。
var testParams = Params{Memory: 1024, Time: 1, Threads: 1, KeyLen: 32, SaltLen: 16}

func TestLegacyArgon2Compat(t *testing.T) {
	for _, tc := range []struct {
		name, password, hash string
		rehash               bool
	}{
		{"legacy", legacyPassword, legacyHash, false},
		{"legacy utf8", legacyUTF8Password, legacyUTF8Hash, false},
		{"cffi default p=4", legacyPassword, cffiDefaultHash, true},
	} {
		ok, rehash, err := VerifyPassword(tc.password, tc.hash, DefaultParams)
		if err != nil || !ok {
			t.Fatalf("%s：旧哈希校验失败 ok=%v err=%v", tc.name, ok, err)
		}
		if rehash != tc.rehash {
			t.Errorf("%s：needsRehash = %v, want %v", tc.name, rehash, tc.rehash)
		}
		if ok, _, _ := VerifyPassword(tc.password+"x", tc.hash, DefaultParams); ok {
			t.Errorf("%s：错误的密码不应通过", tc.name)
		}
	}
}

func TestDefaultParamsMatchLegacy(t *testing.T) {
	want := Params{Memory: 65536, Time: 3, Threads: 2, KeyLen: 32, SaltLen: 16}
	if DefaultParams != want {
		t.Fatalf("DefaultParams 必须与旧 server.py 一致：%+v", DefaultParams)
	}
	d, err := decodeHash(legacyHash)
	if err != nil || d.params != DefaultParams {
		t.Fatalf("解析出的旧哈希参数应等于 DefaultParams：%+v %v", d.params, err)
	}
}

func TestHashRoundTrip(t *testing.T) {
	h, err := HashPassword("新密码 with spaces", testParams)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=1024,t=1,p=1$") {
		t.Fatalf("PHC 格式不对：%s", h)
	}
	ok, rehash, err := VerifyPassword("新密码 with spaces", h, testParams)
	if !ok || rehash || err != nil {
		t.Fatalf("自己生成的哈希应能校验：%v %v %v", ok, rehash, err)
	}
	if _, rehash, _ := VerifyPassword("新密码 with spaces", h, DefaultParams); !rehash {
		t.Fatal("参数不同时应提示重新哈希")
	}
	h2, _ := HashPassword("新密码 with spaces", testParams)
	if h == h2 {
		t.Fatal("每次哈希的盐应该不同")
	}
}

func TestDecodeRejectsMalformed(t *testing.T) {
	for _, bad := range []string{
		"",
		"plain",
		"$argon2d$v=19$m=65536,t=3,p=2$c2FsdHNhbHQ$aGFzaGhhc2g",      // Go 不支持 argon2d
		"$argon2id$v=16$m=65536,t=3,p=2$c2FsdHNhbHQ$aGFzaGhhc2g",     // 旧版本号
		"$argon2id$v=19$m=65536,t=3$c2FsdHNhbHQ$aGFzaGhhc2g",         // 缺 p
		"$argon2id$v=19$m=65536,t=3,p=0$c2FsdHNhbHQ$aGFzaGhhc2g",     // p=0
		"$argon2id$v=19$m=99999999,t=3,p=2$c2FsdHNhbHQ$aGFzaGhhc2g",  // 内存过大
		"$argon2id$v=19$m=65536,t=3,p=2,x=1$c2FsdHNhbHQ$aGFzaGhhc2g", // 未知参数
		"$argon2id$v=19$m=65536,t=3,p=2$!!!$aGFzaGhhc2g",             // salt 不是 base64
		"$argon2id$v=19$m=65536,t=3,p=2$c2FsdHNhbHQ$",                // 缺 hash
		"$scrypt$ln=16,r=8,p=1$c2FsdHNhbHQ$aGFzaGhhc2g",
	} {
		if _, _, err := VerifyPassword("x", bad, DefaultParams); err == nil {
			t.Errorf("%q 应解析失败", bad)
		}
	}
	// 带填充的 base64 也接受
	padded := strings.Replace(legacyHash, "$8CXQOgjZR2VGrLN7x7Xsgg$", "$8CXQOgjZR2VGrLN7x7Xsgg==$", 1)
	if ok, _, err := VerifyPassword(legacyPassword, padded, DefaultParams); !ok || err != nil {
		t.Fatalf("带填充的 salt 应能解析：%v %v", ok, err)
	}
}

func TestToken(t *testing.T) {
	tok, hash := NewToken()
	if !strings.HasPrefix(tok, "mfs_") || len(tok) != 4+43 {
		t.Fatalf("令牌格式不对：%s", tok)
	}
	if !ValidTokenFormat(tok) {
		t.Fatal("自己生成的令牌格式应合法")
	}
	if string(hash) == tok || len(hash) != 32 || string(HashToken(tok)) != string(hash) {
		t.Fatal("库里存的应是 sha256(token)")
	}
	tok2, _ := NewToken()
	if tok == tok2 {
		t.Fatal("令牌重复")
	}
	for _, bad := range []string{"", "mfs_", "abc", "mfs_" + strings.Repeat("A", 42), "xxx_" + tok[4:], "mfs_" + strings.Repeat("!", 43)} {
		if ValidTokenFormat(bad) {
			t.Errorf("%q 不应通过格式检查", bad)
		}
	}
}

func TestResetCode(t *testing.T) {
	c := NewResetCode()
	if len(c) != len("RST-XXXXX-XXXXX-XXXXX") || !strings.HasPrefix(c, "RST-") {
		t.Fatalf("重置码格式不对：%s", c)
	}
	if string(HashResetCode(c)) != string(HashResetCode("  "+strings.ToLower(c)+"\n")) {
		t.Fatal("重置码应不区分大小写、忽略首尾空白")
	}
	if NewResetCode() == c {
		t.Fatal("重置码重复")
	}
}

func TestLockoutPolicy(t *testing.T) {
	l := NewLockout(DefaultLockout)
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := t0
	for i := 1; i <= 4; i++ {
		if _, locked := l.Fail("Alice", now); locked {
			t.Fatalf("第 %d 次失败不应锁定", i)
		}
	}
	d, locked := l.Fail("alice", now) // 不区分大小写
	if !locked || d != 15*time.Minute {
		t.Fatalf("第 5 次失败应锁定 15 分钟：%v %v", d, locked)
	}
	if d, locked := l.Check("ALICE", now.Add(14*time.Minute)); !locked || d != time.Minute {
		t.Fatalf("锁定期内应报剩余时间：%v %v", d, locked)
	}
	// 锁定过期后再失败：锁定时间翻倍
	now = now.Add(15 * time.Minute)
	if _, locked := l.Check("alice", now); locked {
		t.Fatal("15 分钟后应解锁")
	}
	want := []time.Duration{30 * time.Minute, time.Hour, 2 * time.Hour, 4 * time.Hour, 8 * time.Hour, 16 * time.Hour, 24 * time.Hour, 24 * time.Hour}
	for i, w := range want {
		d, locked := l.Fail("alice", now)
		if !locked || d != w {
			t.Fatalf("第 %d 次翻倍：%v, want %v", i+1, d, w)
		}
		now = now.Add(d)
	}
	// 成功登录清零
	l.Reset("alice")
	if _, locked := l.Fail("alice", now); locked {
		t.Fatal("Reset 后应重新计数")
	}
	// 太久以前的失败不再累计
	l2 := NewLockout(DefaultLockout)
	for i := 0; i < 4; i++ {
		l2.Fail("bob", t0)
	}
	if _, locked := l2.Fail("bob", t0.Add(25*time.Hour)); locked {
		t.Fatal("24 小时前的失败应被遗忘")
	}
	l2.Sweep(t0.Add(50 * time.Hour))
	if l2.Len() != 0 {
		t.Fatal("Sweep 应清理过期记录")
	}
}
