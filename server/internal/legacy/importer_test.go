package legacy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"moefrp/server/internal/auth"
	"moefrp/server/internal/store"
	"moefrp/shared/model"
	"moefrp/shared/validate"
)

// 旧 server.py 的 init_db 建表语句（去掉了 P2P 表）。
const legacySchema = `
CREATE TABLE users (uuid TEXT PRIMARY KEY, nickname TEXT UNIQUE NOT NULL, password_hash TEXT NOT NULL,
  current_session_token TEXT, session_token_expiry TEXT, role TEXT NOT NULL DEFAULT 'user');
CREATE TABLE personal_configs (config_id TEXT PRIMARY KEY, owner_uuid TEXT NOT NULL, profile_name TEXT NOT NULL, config_json TEXT NOT NULL);
CREATE TABLE shares (share_id TEXT PRIMARY KEY, owner_uuid TEXT NOT NULL, share_name TEXT NOT NULL, is_template BOOLEAN NOT NULL, config_data_json TEXT NOT NULL);
CREATE TABLE subscriptions (subscription_id TEXT PRIMARY KEY, user_uuid TEXT NOT NULL, share_id TEXT NOT NULL, user_params_json TEXT);
CREATE TABLE invitation_codes (code TEXT PRIMARY KEY, is_used BOOLEAN NOT NULL DEFAULT 0, created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP, used_by_uuid TEXT, used_at TIMESTAMP);
CREATE TABLE password_reset_tokens (token TEXT PRIMARY KEY, user_uuid TEXT NOT NULL, expires_at TEXT NOT NULL);
`

const (
	aliceID = "11111111-1111-4111-8111-111111111111"
	bobID   = "22222222-2222-4222-8222-222222222222"
	carlID  = "33333333-3333-4333-8333-333333333333"
)

func newLegacyDB(t *testing.T, aliceHash string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "users.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(legacySchema); err != nil {
		t.Fatal(err)
	}
	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	mustExec(`INSERT INTO users (uuid, nickname, password_hash, role) VALUES (?, ?, ?, ?)`, aliceID, "Alice", aliceHash, "vip")
	mustExec(`INSERT INTO users (uuid, nickname, password_hash, role) VALUES (?, ?, ?, ?)`, bobID, "alice", aliceHash, "user") // 只差大小写
	mustExec(`INSERT INTO users (uuid, nickname, password_hash, role) VALUES (?, ?, ?, ?)`, carlID, "carl_9", "$argon2d$v=19$m=1,t=1,p=1$x$y", "weird")

	cfg := map[string]any{
		"nodes": []map[string]any{
			{"remark": "上海 &amp; 杭州", "server_addr": "frp.example.com", "server_port": "7000", "token": "tok"},
			{"remark": "坏端口", "server_addr": "x.example.com", "server_port": "abc", "token": ""},
		},
		"proxies": []map[string]any{
			{"enabled": true, "name": "ssh", "type": "tcp", "local_ip": "127.0.0.1", "local_port": "22", "remote_port": "6000", "custom_domains": "", "sk": ""},
			{"enabled": false, "name": "web", "type": "http", "local_ip": "", "local_port": "80", "remote_port": "", "custom_domains": "a.example.com, b.example.com", "sk": ""},
			{"name": "p2p", "type": "stcp", "local_ip": "127.0.0.1", "local_port": "3389", "sk": "", "stun_server": "stun.example.com:3478"},
			{"name": "ssh", "type": "tcp", "local_ip": "127.0.0.1", "local_port": "23"},
			{"name": "bad", "type": "socks", "local_port": "1"},
		},
	}
	blob, _ := json.Marshal(cfg)
	mustExec(`INSERT INTO personal_configs VALUES (?, ?, ?, ?)`, "conf-"+aliceID, aliceID, "我的云端配置", string(blob))
	mustExec(`INSERT INTO personal_configs VALUES (?, ?, ?, ?)`, "conf-empty", aliceID, "空配置", "{}")
	mustExec(`INSERT INTO personal_configs VALUES (?, ?, ?, ?)`, "conf-single", aliceID, "单节点", `{"serverAddr":"1.2.3.4","serverPort":7001,"auth":{"token":"t"}}`)
	mustExec(`INSERT INTO personal_configs VALUES (?, ?, ?, ?)`, "conf-bob", bobID, "bob 的", "{}")
	mustExec(`INSERT INTO personal_configs VALUES (?, ?, ?, ?)`, "conf-broken", aliceID, "坏的", "{not json")

	valid, _ := validate.MakeInviteCode("ABCDEFG")
	used, _ := validate.MakeInviteCode("HJKMNPQ")
	mustExec(`INSERT INTO invitation_codes (code, is_used, created_at) VALUES (?, 0, '2025-07-01 10:00:00')`, strings.ToLower(valid))
	mustExec(`INSERT INTO invitation_codes (code, is_used, created_at, used_by_uuid, used_at) VALUES (?, 1, '2025-07-01 10:00:00', ?, '2025-07-02 11:00:00')`, used, aliceID)
	bad := valid[:len(valid)-1] + "2"
	if bad == valid {
		bad = valid[:len(valid)-1] + "3"
	}
	mustExec(`INSERT INTO invitation_codes (code, is_used) VALUES (?, 0)`, bad) // 校验位错误
	mustExec(`INSERT INTO shares VALUES ('s1', ?, 'x', 1, '{}')`, aliceID)      // 没有节点：跳过
	nodes := `[{"remark":"上海","server_addr":"frp.example.com","server_port":"7000","token":"tok"},{"remark":"香港","server_addr":"hk.example.com","server_port":7001,"token":"tok"}]`
	mustExec(`INSERT INTO shares VALUES ('share-tpl', ?, '模板 &amp; 节点', 1, ?)`, aliceID, `{"nodes":`+nodes+`}`)
	mustExec(`INSERT INTO shares VALUES ('share-full', ?, '完整', 0, ?)`, aliceID,
		`{"nodes":`+nodes+`,"proxies":[{"name":"ssh","type":"tcp","local_ip":"127.0.0.1","local_port":"22","remote_port":"6000"}]}`)
	mustExec(`INSERT INTO shares VALUES ('share-p2p', ?, '远程桌面', 0, ?)`, aliceID,
		`{"nodes":`+nodes+`,"proxies":[{"name":"rdp","type":"stcp","local_ip":"127.0.0.1","local_port":"3389","sk":"realkey"}]}`)
	mustExec(`INSERT INTO subscriptions VALUES ('sub-tpl', ?, 'share-tpl', ?)`, carlID,
		`{"proxies":[{"name":"web","type":"tcp","local_port":"80","remote_port":"8080"}],"node_remark":"香港"}`)
	mustExec(`INSERT INTO subscriptions VALUES ('sub-full', ?, 'share-full', '{}')`, carlID)
	mustExec(`INSERT INTO subscriptions VALUES ('sub-p2p', ?, 'share-p2p', ?)`, carlID,
		`{"visitors":[{"name":"my-rdp","type":"stcp","serverName":"rdp","secretKey":"realkey","bindPort":13389,"bindAddr":"127.0.0.1"}]}`)
	mustExec(`INSERT INTO subscriptions VALUES ('sub-orphan', ?, 'share-full', '{}')`, bobID) // 订阅者没导入
	return path
}

func newTarget(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "new.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if _, err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestImport(t *testing.T) {
	ctx := context.Background()
	hash, err := auth.HashPassword("correct horse", auth.DefaultParams)
	if err != nil {
		t.Fatal(err)
	}
	legacyPath := newLegacyDB(t, hash)
	st := newTarget(t)

	// 先 dry-run：有报告，库里什么都没有
	rep, err := Import(ctx, legacyPath, st, ImportOptions{DryRun: true, Now: func() int64 { return 1000 }})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Users != 2 || rep.Profiles != 3 {
		t.Fatalf("dry-run report: %+v", rep)
	}
	if n := count(t, st, "users"); n != 0 {
		t.Fatalf("dry-run wrote %d users", n)
	}

	rep, err = Import(ctx, legacyPath, st, ImportOptions{Now: func() int64 { return 1000 }})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range rep.Problems {
		t.Log(p)
	}
	// Alice、carl 导入；"alice"（只差大小写）跳过
	if rep.Users != 2 {
		t.Errorf("users = %d, want 2", rep.Users)
	}
	u, err := store.GetUserByNickname(ctx, st.DB(), "Alice")
	if err != nil || u.ID != aliceID || u.Role != "user" {
		t.Fatalf("alice = %+v, %v", u, err)
	}
	ok, _, err := auth.VerifyPassword("correct horse", u.PasswordHash, auth.DefaultParams)
	if err != nil || !ok {
		t.Fatalf("旧哈希无法校验：ok=%v err=%v", ok, err)
	}
	carl, err := store.GetUserByNickname(ctx, st.DB(), "carl_9")
	if err != nil || carl.Role != "user" {
		t.Fatalf("carl = %+v, %v", carl, err)
	}

	// 配置：主配置、空配置、单节点；bob 的（所有者没导入）和坏 JSON 跳过
	if rep.Profiles != 3 {
		t.Errorf("profiles = %d, want 3", rep.Profiles)
	}
	rows, err := store.ListProfiles(ctx, st.DB(), aliceID)
	if err != nil {
		t.Fatal(err)
	}
	var main, single *store.ProfileRow
	for i := range rows {
		switch rows[i].Name {
		case "我的云端配置":
			main = &rows[i]
		case "单节点":
			single = &rows[i]
		}
	}
	if main == nil || single == nil {
		t.Fatalf("profiles: %+v", rows)
	}
	var d model.ProfileData
	if err := json.Unmarshal([]byte(main.Data), &d); err != nil {
		t.Fatal(err)
	}
	if err := validate.ProfileData(d); err != nil {
		t.Fatalf("导入后的配置不合法: %v", err)
	}
	if len(d.Nodes) != 1 || d.Nodes[0].Remark != "上海 & 杭州" || d.Nodes[0].ServerPort != 7000 {
		t.Errorf("nodes = %+v", d.Nodes)
	}
	if len(d.Proxies) != 3 {
		t.Fatalf("proxies = %+v", d.Proxies)
	}
	byName := map[string]model.Proxy{}
	for _, p := range d.Proxies {
		byName[p.Name] = p
		if p.ID == "" {
			t.Errorf("proxy %s has no ID", p.Name)
		}
	}
	if p := byName["ssh"]; p.LocalPort != 22 || p.RemotePort != 6000 || !p.Enabled {
		t.Errorf("ssh = %+v", p)
	}
	if p := byName["web"]; p.Enabled || p.LocalIP != "127.0.0.1" || len(p.CustomDomains) != 2 || p.CustomDomains[1] != "b.example.com" {
		t.Errorf("web = %+v", p)
	}
	if p := byName["p2p"]; len(p.SecretKey) < 32 {
		t.Errorf("p2p secret not generated: %+v", p)
	}
	var sd model.ProfileData
	json.Unmarshal([]byte(single.Data), &sd)
	if len(sd.Nodes) != 1 || sd.Nodes[0].ServerPort != 7001 || sd.Nodes[0].Token != "t" {
		t.Errorf("single = %+v", sd.Nodes)
	}

	// 邀请码：两个合法（一个已用），一个校验位错误跳过
	if rep.Invites != 2 || rep.InvitesUsed != 1 {
		t.Errorf("invites = %d used = %d", rep.Invites, rep.InvitesUsed)
	}
	invs, err := store.ListInvites(ctx, st.DB(), true)
	if err != nil || len(invs) != 1 {
		t.Fatalf("unused invites = %+v, %v", invs, err)
	}

	// 分享与订阅
	if rep.Shares != 4 || rep.SharesImported != 3 || !hasProblem(rep, "shares") {
		t.Errorf("shares: %+v", rep)
	}
	if rep.Subscriptions != 4 || rep.SubscriptionsImported != 3 {
		t.Errorf("subscriptions: imported %d of %d", rep.SubscriptionsImported, rep.Subscriptions)
	}
	kinds := map[string]string{}
	shares, err := store.ListShares(ctx, st.DB(), aliceID)
	if err != nil {
		t.Fatal(err)
	}
	for _, sh := range shares {
		kinds[sh.ID] = sh.Kind
		var payload model.SharePayload
		json.Unmarshal([]byte(sh.Payload), &payload)
		if err := validate.SharePayload(sh.Kind, payload); err != nil {
			t.Errorf("share %s invalid: %v", sh.ID, err)
		}
		if sh.ID == "share-tpl" && sh.Name != "模板 & 节点" {
			t.Errorf("share name not unescaped: %q", sh.Name)
		}
		if sh.ID == "share-p2p" && (len(payload.Nodes) != 1 || len(payload.Targets) != 1 || payload.Targets[0].SecretKey != "realkey") {
			t.Errorf("p2p payload: %+v", payload)
		}
	}
	if kinds["share-tpl"] != model.ShareTemplate || kinds["share-full"] != model.ShareFull || kinds["share-p2p"] != model.ShareP2P {
		t.Errorf("kinds: %v", kinds)
	}
	subs, err := store.ListSubscriptions(ctx, st.DB(), carlID)
	if err != nil || len(subs) != 3 {
		t.Fatalf("carl subs: %v %v", len(subs), err)
	}
	for _, sub := range subs {
		var params model.SubscriptionParams
		json.Unmarshal([]byte(sub.Params), &params)
		switch sub.ShareID {
		case "share-tpl":
			if len(params.Proxies) != 1 || params.Proxies[0].Name != "web" || params.NodeID == "" {
				t.Errorf("tpl params: %+v", params)
			}
		case "share-p2p":
			if len(params.Binds) != 1 || params.Binds[0].BindPort != 13389 || params.Binds[0].Name != "my-rdp" {
				t.Errorf("p2p params: %+v", params)
			}
		}
	}
	for _, want := range []string{"只差大小写", "argon2id", "未知角色", "所有者", "无法解析", "重复", "secretKey", "stun_server", "不支持", "端口"} {
		if !hasReason(rep, want) {
			t.Errorf("report missing problem containing %q", want)
		}
	}

	// 已有数据的库拒绝再次导入
	if _, err := Import(ctx, legacyPath, st, ImportOptions{}); !errors.Is(err, ErrTargetNotEmpty) {
		t.Errorf("second import err = %v", err)
	}
}

func TestImportRejectsNonLegacy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.db")
	db, _ := sql.Open("sqlite", path)
	db.Exec(`CREATE TABLE foo (x)`)
	db.Close()
	if _, err := Import(context.Background(), path, newTarget(t), ImportOptions{}); err == nil {
		t.Fatal("expected error")
	}
}

func count(t *testing.T, st *store.Store, table string) int {
	var n int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func hasProblem(rep Report, table string) bool {
	for _, p := range rep.Problems {
		if p.Table == table {
			return true
		}
	}
	return false
}

func hasReason(rep Report, s string) bool {
	for _, p := range rep.Problems {
		if strings.Contains(p.Reason, s) {
			return true
		}
	}
	return false
}
