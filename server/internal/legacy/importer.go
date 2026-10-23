package legacy

// 旧版 users.db 导入（PLAN 7.12）。
//
// 导入 users、personal_configs、invitation_codes、shares、subscriptions（后两者见 shares.go）。
// p2p_* 表、会话、password_reset_tokens 不迁移。
//
// 整个导入在一个事务里完成；DryRun 时最后回滚，只输出报告。目标库里已经有用户时拒绝导入，
// 避免和线上数据混在一起。

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"moefrp/server/internal/store"
	"moefrp/shared/model"
	"moefrp/shared/validate"
)

// AuditImport 是导入完成后写入 audit_log 的动作名。
const AuditImport = "admin.import_legacy"

// ImportOptions 控制导入行为。
type ImportOptions struct {
	DryRun bool
	// Now 返回当前 Unix 秒，测试时可替换。
	Now func() int64
}

// Problem 是导入时发现的一个问题。Skipped 为 true 表示对应的条目没有导入。
type Problem struct {
	Table   string
	Key     string
	Reason  string
	Skipped bool
}

func (p Problem) String() string {
	mark := "警告"
	if p.Skipped {
		mark = "跳过"
	}
	return fmt.Sprintf("[%s] %s %s：%s", mark, p.Table, p.Key, p.Reason)
}

// Report 是导入结果的统计。
type Report struct {
	DryRun                bool
	Users                 int
	Profiles              int
	Proxies               int
	Nodes                 int
	Invites               int
	InvitesUsed           int
	Shares                int // 旧库里的分享条数
	Subscriptions         int // 旧库里的订阅条数
	SharesImported        int
	SubscriptionsImported int
	Problems              []Problem
}

func (r *Report) problem(table, key string, skipped bool, format string, args ...any) {
	r.Problems = append(r.Problems, Problem{Table: table, Key: key, Reason: fmt.Sprintf(format, args...), Skipped: skipped})
}

// SkippedCount 返回被跳过的条目数。
func (r Report) SkippedCount() int {
	n := 0
	for _, p := range r.Problems {
		if p.Skipped {
			n++
		}
	}
	return n
}

// ErrTargetNotEmpty 表示目标库里已经有用户。
var ErrTargetNotEmpty = errors.New("目标数据库里已经有用户，拒绝导入（只能导入到全新的库）")

// Import 从旧库 legacyPath 导入数据到 dst。
func Import(ctx context.Context, legacyPath string, dst *store.Store, opts ImportOptions) (Report, error) {
	rep := Report{DryRun: opts.DryRun}
	now := opts.Now
	if now == nil {
		now = func() int64 { return time.Now().Unix() }
	}

	src, err := openLegacy(legacyPath)
	if err != nil {
		return rep, err
	}
	defer src.Close()

	users, err := readUsers(ctx, src)
	if err != nil {
		return rep, fmt.Errorf("读取旧 users 表：%w", err)
	}
	configs, err := readConfigs(ctx, src)
	if err != nil {
		return rep, fmt.Errorf("读取旧 personal_configs 表：%w", err)
	}
	invites, err := readInvites(ctx, src)
	if err != nil {
		return rep, fmt.Errorf("读取旧 invitation_codes 表：%w", err)
	}
	shares, err := readShares(ctx, src)
	if err != nil {
		return rep, fmt.Errorf("读取旧 shares 表：%w", err)
	}
	subs, err := readSubscriptions(ctx, src)
	if err != nil {
		return rep, fmt.Errorf("读取旧 subscriptions 表：%w", err)
	}
	rep.Shares, rep.Subscriptions = len(shares), len(subs)

	errDryRun := errors.New("dry run")
	err = dst.Tx(ctx, func(tx *sql.Tx) error {
		var existing int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&existing); err != nil {
			return err
		}
		if existing > 0 {
			return ErrTargetNotEmpty
		}

		ts := now()
		imported := map[string]bool{} // 已导入的用户 ID
		seenNick := map[string]string{}
		for _, u := range users {
			if !importUser(ctx, tx, &rep, u, ts, imported, seenNick) {
				continue
			}
		}
		if err := ctxErr(ctx); err != nil {
			return err
		}

		perUser := map[string]int{}
		for _, c := range configs {
			if err := importConfig(ctx, tx, &rep, c, ts, imported, perUser); err != nil {
				return err
			}
		}
		for _, inv := range invites {
			if err := importInvite(ctx, tx, &rep, inv, ts, imported); err != nil {
				return err
			}
		}
		imShares, err := importShares(ctx, tx, &rep, shares, subs, ts, imported)
		if err != nil {
			return err
		}
		if err := importSubscriptions(ctx, tx, &rep, subs, ts, imported, imShares); err != nil {
			return err
		}

		if err := store.InsertAudit(ctx, tx, store.AuditEntry{
			At: ts, Action: AuditImport,
			Detail: map[string]any{
				"users": rep.Users, "profiles": rep.Profiles, "invites": rep.Invites,
				"shares": rep.SharesImported, "subscriptions": rep.SubscriptionsImported,
				"skipped": rep.SkippedCount(), "problems": len(rep.Problems),
			},
		}); err != nil {
			return err
		}
		if opts.DryRun {
			return errDryRun
		}
		return nil
	})
	if errors.Is(err, errDryRun) {
		err = nil
	}
	return rep, err
}

func ctxErr(ctx context.Context) error { return ctx.Err() }

func openLegacy(path string) (*sql.DB, error) {
	// 只读打开，绝不修改旧库。
	u := url.URL{Scheme: "file", Opaque: path, RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("打开旧库 %s：%w", path, err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='users'`).Scan(&n); err != nil {
		db.Close()
		return nil, err
	}
	if n == 0 {
		db.Close()
		return nil, fmt.Errorf("%s 不是旧版 users.db（没有 users 表）", path)
	}
	return db, nil
}

func countRows(ctx context.Context, db *sql.DB, table string) (int, error) {
	var exists int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&exists); err != nil {
		return 0, err
	}
	if exists == 0 {
		return 0, nil
	}
	var n int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table).Scan(&n)
	return n, err
}

// ---------- users ----------

type legacyUser struct {
	UUID, Nickname, PasswordHash, Role string
}

func readUsers(ctx context.Context, db *sql.DB) ([]legacyUser, error) {
	rows, err := db.QueryContext(ctx, `SELECT uuid, nickname, password_hash, COALESCE(role, 'user') FROM users ORDER BY rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []legacyUser
	for rows.Next() {
		var u legacyUser
		if err := rows.Scan(&u.UUID, &u.Nickname, &u.PasswordHash, &u.Role); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func importUser(ctx context.Context, tx *sql.Tx, rep *Report, u legacyUser, ts int64, imported map[string]bool, seenNick map[string]string) bool {
	key := u.Nickname
	if err := validate.ID("id", u.UUID); err != nil {
		rep.problem("users", key, true, "uuid %q 不是合法 ID", u.UUID)
		return false
	}
	if !validate.IsNicknameFormat(u.Nickname) {
		rep.problem("users", key, true, "昵称不符合 3–20 位字母、数字、下划线的规则")
		return false
	}
	lower := strings.ToLower(u.Nickname)
	if first, ok := seenNick[lower]; ok {
		rep.problem("users", key, true, "昵称与 %q 只差大小写，新版昵称不区分大小写", first)
		return false
	}
	if validate.IsReservedNickname(u.Nickname) {
		rep.problem("users", key, false, "昵称在新版里是保留词，已照常导入（新用户不能再注册这个昵称）")
	}
	if !strings.HasPrefix(u.PasswordHash, "$argon2id$v=19$") {
		rep.problem("users", key, false, "密码哈希不是 argon2id v=19，新版无法校验，需要用 admin user reset-code 重置")
	}
	role := u.Role
	switch role {
	case "user", "vip", "admin":
	default:
		rep.problem("users", key, false, "未知角色 %q，已改为 user", role)
		role = "user"
	}
	err := store.InsertUser(ctx, tx, store.User{
		ID: u.UUID, Nickname: u.Nickname, PasswordHash: u.PasswordHash,
		Role: role, Status: "active", CreatedAt: ts, UpdatedAt: ts,
	})
	if err != nil {
		rep.problem("users", key, true, "写入失败：%v", err)
		return false
	}
	seenNick[lower] = u.Nickname
	imported[u.UUID] = true
	rep.Users++
	return true
}

// ---------- personal_configs ----------

type legacyConfig struct {
	ID, Owner, Name, JSON string
}

func readConfigs(ctx context.Context, db *sql.DB) ([]legacyConfig, error) {
	rows, err := db.QueryContext(ctx, `SELECT config_id, owner_uuid, profile_name, config_json FROM personal_configs ORDER BY rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []legacyConfig
	for rows.Next() {
		var c legacyConfig
		if err := rows.Scan(&c.ID, &c.Owner, &c.Name, &c.JSON); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// flexString 接受 JSON 里的字符串、数字或 null。
type flexString string

func (f *flexString) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "null" {
		*f = ""
		return nil
	}
	if strings.HasPrefix(s, `"`) {
		var v string
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
		*f = flexString(v)
		return nil
	}
	*f = flexString(s)
	return nil
}

func (f flexString) str() string { return strings.TrimSpace(html.UnescapeString(string(f))) }

func (f flexString) port() (int, bool) {
	s := f.str()
	if s == "" {
		return 0, true
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		if fl, ferr := strconv.ParseFloat(s, 64); ferr == nil && fl == float64(int(fl)) {
			return int(fl), true
		}
		return 0, false
	}
	return n, true
}

// flexDomains 接受逗号分隔的字符串或字符串数组。
type flexDomains []string

func (f *flexDomains) UnmarshalJSON(b []byte) error {
	var list []string
	if err := json.Unmarshal(b, &list); err == nil {
		*f = list
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		*f = nil
		return nil
	}
	*f = strings.Split(s, ",")
	return nil
}

type legacyNode struct {
	Remark     flexString `json:"remark"`
	ServerAddr flexString `json:"server_addr"`
	ServerPort flexString `json:"server_port"`
	Token      flexString `json:"token"`
}

type legacyProxy struct {
	Enabled       *bool       `json:"enabled"`
	Name          flexString  `json:"name"`
	Type          flexString  `json:"type"`
	LocalIP       flexString  `json:"local_ip"`
	LocalPort     flexString  `json:"local_port"`
	RemotePort    flexString  `json:"remote_port"`
	CustomDomains flexDomains `json:"custom_domains"`
	SK            flexString  `json:"sk"`
	STUNServer    flexString  `json:"stun_server"`
}

type legacyConfigJSON struct {
	Nodes   []legacyNode  `json:"nodes"`
	Proxies []legacyProxy `json:"proxies"`
	// 更早的单节点写法
	ServerAddr  flexString `json:"serverAddr"`
	ServerAddr2 flexString `json:"server_addr"`
	ServerPort  flexString `json:"serverPort"`
	ServerPort2 flexString `json:"server_port"`
	Remark      flexString `json:"remark"`
	Token       flexString `json:"token"`
	Auth        struct {
		Token flexString `json:"token"`
	} `json:"auth"`
}

func importConfig(ctx context.Context, tx *sql.Tx, rep *Report, c legacyConfig, ts int64, imported map[string]bool, perUser map[string]int) error {
	name := strings.TrimSpace(html.UnescapeString(c.Name))
	key := fmt.Sprintf("%s（%s）", c.ID, name)
	if !imported[c.Owner] {
		rep.problem("personal_configs", key, true, "所有者 %s 没有导入", c.Owner)
		return nil
	}
	if perUser[c.Owner] >= validate.MaxProfilesPerUser {
		rep.problem("personal_configs", key, true, "该用户的配置超过 %d 个", validate.MaxProfilesPerUser)
		return nil
	}
	if validate.ProfileName(name) != nil {
		if name == "" {
			name = "导入的配置"
		} else {
			name = truncateRunes(name, validate.MaxProfileNameLen)
		}
		rep.problem("personal_configs", key, false, "配置名不合法，已改为 %q", name)
	}

	data, err := convertConfigJSON(rep, key, c.JSON)
	if err != nil {
		rep.problem("personal_configs", key, true, "config_json 无法解析：%v", err)
		return nil
	}
	if err := validate.ProfileData(data); err != nil {
		rep.problem("personal_configs", key, true, "转换后仍不合法：%v", err)
		return nil
	}
	blob, err := json.Marshal(data)
	if err != nil {
		return err
	}
	id := c.ID
	if validate.ID("id", id) != nil {
		id = model.NewID()
	}
	err = store.InsertProfile(ctx, tx, store.ProfileRow{
		ID: id, OwnerID: c.Owner, Name: name, Data: string(blob),
		SchemaVersion: store.CurrentProfileSchema, Revision: 1, CreatedAt: ts, UpdatedAt: ts,
	})
	if store.IsUniqueViolation(err) {
		id = model.NewID()
		err = store.InsertProfile(ctx, tx, store.ProfileRow{
			ID: id, OwnerID: c.Owner, Name: name, Data: string(blob),
			SchemaVersion: store.CurrentProfileSchema, Revision: 1, CreatedAt: ts, UpdatedAt: ts,
		})
	}
	if err != nil {
		return err
	}
	if _, err := store.BumpStateVersion(ctx, tx, c.Owner); err != nil {
		return err
	}
	perUser[c.Owner]++
	rep.Profiles++
	rep.Nodes += len(data.Nodes)
	rep.Proxies += len(data.Proxies)
	return nil
}

// convertConfigJSON 把旧版 config_json 转成新的 ProfileData，逐条丢弃无法修复的节点和代理。
func convertConfigJSON(rep *Report, key, raw string) (model.ProfileData, error) {
	data := model.ProfileData{Nodes: []model.Node{}, Proxies: []model.Proxy{}, Visitors: []model.Visitor{}}
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "{}" || raw == "null" {
		return data, nil
	}
	var lc legacyConfigJSON
	if err := json.Unmarshal([]byte(raw), &lc); err != nil {
		return data, err
	}

	nodes := lc.Nodes
	if len(nodes) == 0 {
		addr := lc.ServerAddr
		if addr.str() == "" {
			addr = lc.ServerAddr2
		}
		if addr.str() != "" {
			port := lc.ServerPort
			if port.str() == "" {
				port = lc.ServerPort2
			}
			token := lc.Auth.Token
			if token.str() == "" {
				token = lc.Token
			}
			remark := lc.Remark
			if remark.str() == "" {
				remark = addr
			}
			nodes = []legacyNode{{Remark: remark, ServerAddr: addr, ServerPort: port, Token: token}}
		}
	}

	for i, ln := range nodes {
		port, ok := ln.ServerPort.port()
		if !ok {
			rep.problem("personal_configs", key, false, "节点 %d 的端口 %q 不是数字，已丢弃该节点", i+1, ln.ServerPort.str())
			continue
		}
		if port == 0 {
			port = 7000
		}
		remark := ln.Remark.str()
		if remark == "" {
			remark = ln.ServerAddr.str()
		}
		n := model.Node{
			ID: model.NewID(), Remark: truncateRunes(remark, validate.MaxRemarkLen),
			ServerAddr: ln.ServerAddr.str(), ServerPort: port, Token: string(ln.Token),
		}
		if err := validate.ProfileData(model.ProfileData{Nodes: []model.Node{n}}); err != nil {
			rep.problem("personal_configs", key, false, "节点 %q 不合法，已丢弃：%v", remark, err)
			continue
		}
		data.Nodes = append(data.Nodes, n)
		if len(data.Nodes) == validate.MaxNodesPerProfile {
			break
		}
	}

	names := map[string]bool{}
	for i, lp := range lc.Proxies {
		p, ok := convertProxy(rep, key, i, lp)
		if !ok {
			continue
		}
		if names[p.Name] {
			rep.problem("personal_configs", key, false, "代理名 %q 重复，已丢弃后出现的那个", p.Name)
			continue
		}
		if err := validate.ProfileData(model.ProfileData{Proxies: []model.Proxy{p}}); err != nil {
			rep.problem("personal_configs", key, false, "代理 %q 不合法，已丢弃：%v", p.Name, err)
			continue
		}
		names[p.Name] = true
		data.Proxies = append(data.Proxies, p)
		if len(data.Proxies) == validate.MaxProxiesPerProfile {
			break
		}
	}
	return data, nil
}

func convertProxy(rep *Report, key string, i int, lp legacyProxy) (model.Proxy, bool) {
	name := lp.Name.str()
	label := name
	if label == "" {
		label = fmt.Sprintf("#%d", i+1)
	}
	typ := strings.ToLower(lp.Type.str())
	if !slices.Contains(model.ProxyTypes, typ) {
		rep.problem("personal_configs", key, false, "代理 %s 的类型 %q 不支持，已丢弃", label, typ)
		return model.Proxy{}, false
	}
	localPort, ok := lp.LocalPort.port()
	if !ok {
		rep.problem("personal_configs", key, false, "代理 %s 的本地端口 %q 不是数字，已丢弃", label, lp.LocalPort.str())
		return model.Proxy{}, false
	}
	remotePort, ok := lp.RemotePort.port()
	if !ok {
		rep.problem("personal_configs", key, false, "代理 %s 的远程端口 %q 不是数字，已改为由服务端分配", label, lp.RemotePort.str())
		remotePort = 0
	}
	enabled := true
	if lp.Enabled != nil {
		enabled = *lp.Enabled
	}
	localIP := lp.LocalIP.str()
	if localIP == "" {
		localIP = "127.0.0.1"
	}
	p := model.Proxy{
		ID: model.NewID(), Name: name, Type: typ, Enabled: enabled,
		LocalIP: localIP, LocalPort: localPort,
	}
	switch typ {
	case model.ProxyHTTP, model.ProxyHTTPS, model.ProxyTCPMux:
		for _, d := range lp.CustomDomains {
			if d = strings.TrimSpace(html.UnescapeString(d)); d != "" {
				p.CustomDomains = append(p.CustomDomains, d)
			}
		}
	default:
		p.RemotePort = remotePort
	}
	if model.IsP2PType(typ) {
		p.SecretKey = lp.SK.str()
		if p.SecretKey == "" {
			p.SecretKey = randomSecret()
			rep.problem("personal_configs", key, false, "代理 %s 没有 secretKey，已随机生成，访问端需要重新配置", label)
		}
	}
	if lp.STUNServer.str() != "" {
		rep.problem("personal_configs", key, false, "代理 %s 的 stun_server 不再支持，已忽略", label)
	}
	return p, true
}

func randomSecret() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}

// ---------- invitation_codes ----------

type legacyInvite struct {
	Code      string
	IsUsed    bool
	CreatedAt sql.NullString
	UsedBy    sql.NullString
	UsedAt    sql.NullString
}

func readInvites(ctx context.Context, db *sql.DB) ([]legacyInvite, error) {
	rows, err := db.QueryContext(ctx, `SELECT code, COALESCE(is_used, 0), CAST(created_at AS TEXT), used_by_uuid, CAST(used_at AS TEXT) FROM invitation_codes ORDER BY rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []legacyInvite
	for rows.Next() {
		var inv legacyInvite
		if err := rows.Scan(&inv.Code, &inv.IsUsed, &inv.CreatedAt, &inv.UsedBy, &inv.UsedAt); err != nil {
			return nil, err
		}
		out = append(out, inv)
	}
	return out, rows.Err()
}

// parseLegacyTime 解析 SQLite CURRENT_TIMESTAMP 的格式（UTC），失败时返回 fallback。
func parseLegacyTime(s sql.NullString, fallback int64) int64 {
	if !s.Valid || strings.TrimSpace(s.String) == "" {
		return fallback
	}
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02T15:04:05Z07:00", "2006-01-02 15:04:05.999999"} {
		if t, err := time.Parse(layout, strings.TrimSpace(s.String)); err == nil {
			return t.Unix()
		}
	}
	return fallback
}

func importInvite(ctx context.Context, tx *sql.Tx, rep *Report, inv legacyInvite, ts int64, imported map[string]bool) error {
	code := validate.NormalizeInviteCode(inv.Code)
	if validate.InviteCode(code) != nil {
		rep.problem("invitation_codes", inv.Code, true, "格式或校验位不合法")
		return nil
	}
	created := parseLegacyTime(inv.CreatedAt, ts)
	var usedBy any
	var usedAt any
	used := inv.IsUsed || inv.UsedBy.Valid
	if used {
		usedAt = parseLegacyTime(inv.UsedAt, ts)
		if inv.UsedBy.Valid && imported[inv.UsedBy.String] {
			usedBy = inv.UsedBy.String
		}
	}
	_, err := tx.ExecContext(ctx,
		`INSERT INTO invite_codes (code, note, created_at, used_by, used_at) VALUES (?, ?, ?, ?, ?)`,
		code, "旧版导入", created, usedBy, usedAt)
	if store.IsUniqueViolation(err) {
		rep.problem("invitation_codes", code, true, "重复")
		return nil
	}
	if err != nil {
		return err
	}
	rep.Invites++
	if used {
		rep.InvitesUsed++
	}
	return nil
}
