package nodes

// 节点由面板托管（docs/node-agent.md 第 4 节）：
//   - frps 配置是结构化的 frpsset.Settings，管理后台按 schema 生成表单，agent 在内存里拼出 frps 配置；
//   - frps 不设 token，只认面板签发的登录票据；
//   - agent 把配置和授权表用"存储密钥"加密保存。存储密钥 = HMAC(面板主密钥, 节点 ID + 机器指纹)，
//     面板连上时下发，带一个租约（LeaseTTL）：租约内节点重启、面板离线也能照常启动；
//   - 面板主密钥可以在管理后台轮换：所有票据和节点存储密钥立即换掉，重新下发给在线节点。

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"

	"moefrp/server/internal/store"
	"moefrp/shared/agentproto"
	"moefrp/shared/apierr"
	"moefrp/shared/frpsset"
	"moefrp/shared/nodeticket"
	"moefrp/shared/quota"
)

const (
	// LeaseTTL 是节点本地加密存储的租约：这么久连不上面板，节点重启后就解不开配置，要等面板。
	LeaseTTL = 7 * 24 * time.Hour
	// leaseRenew：在线节点隔这么久续一次租约。
	leaseRenew = 6 * time.Hour

	settingKeyRotatedAt = "node_cred_key_rotated_at"
	settingKeyVersion   = "node_cred_key_version"
)

// settingsOf 解析节点的配置；没有（还没转换）时返回默认配置。
func settingsOf(n store.Node) frpsset.Settings {
	s := frpsset.Defaults()
	if n.Settings != "" {
		_ = json.Unmarshal([]byte(n.Settings), &s)
	}
	return s
}

// managed 判断节点上的 agent 支持托管（结构化配置、加密存储、没有 token）。还没上过线的算支持。
func managed(h *agentproto.Hello) bool {
	return h == nil || h.AgentVersion == "" || hasFeature(h, agentproto.FeatureSettings)
}

// clientInfo 是客户端连接这个节点需要的信息（没有 token：节点只认登录票据）。
func clientInfo(n store.Node) *ClientInfo {
	s := settingsOf(n)
	addr := n.PublicAddr
	if addr == "" {
		addr = n.LastIP
	}
	return &ClientInfo{Addr: addr, Port: s.BindPort}
}

// Schema 是 frps 配置表单的描述。
type Schema struct {
	Groups   []frpsset.Group  `json:"groups"`
	Fields   []frpsset.Field  `json:"fields"`
	Defaults frpsset.Settings `json:"defaults"`
}

// SettingsSchema 返回 frps 配置表单的描述（管理后台据此生成表单）。
func SettingsSchema() Schema {
	return Schema{Groups: frpsset.Groups, Fields: frpsset.Schema(), Defaults: frpsset.Defaults()}
}

// SetSettings 保存节点的 frps 配置；节点在线时立即下发并等待结果。
func (s *Service) SetSettings(ctx context.Context, by Actor, id string, set frpsset.Settings) (ApplyOutcome, error) {
	if err := set.Validate(); err != nil {
		var fe *frpsset.FieldError
		if errors.As(err, &fe) {
			return ApplyOutcome{}, badRequest(fe.Key, fe.Message)
		}
		return ApplyOutcome{}, badRequest("settings", err.Error())
	}
	b, _ := json.Marshal(set)
	var rev int64
	err := s.st.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		if rev, err = store.SetNodeSettings(ctx, tx, id, string(b), s.now().Unix()); err != nil {
			return err
		}
		return s.audit(ctx, tx, by, store.AuditNodeConfig, id, map[string]any{"rev": rev})
	})
	if err != nil {
		return ApplyOutcome{}, notFound(err)
	}
	out := ApplyOutcome{Rev: rev}
	s.mu.Lock()
	c := s.conns[id]
	s.mu.Unlock()
	if c == nil {
		return out, nil
	}
	if !hasFeature(&c.hello, agentproto.FeatureSettings) {
		return out, apierr.Newf(apierr.NodeFailed, "已保存。节点上的 agent（%s）不支持新的配置方式，升级到 1.4.0 后自动生效", c.hello.AgentVersion)
	}
	res, err := s.push(ctx, id, rev, set)
	if err != nil {
		return out, err
	}
	out.Pushed, out.Result = true, &res
	return out, nil
}

// push 下发配置并记录结果。
func (s *Service) push(ctx context.Context, id string, rev int64, set frpsset.Settings) (agentproto.ApplyResult, error) {
	var res agentproto.ApplyResult
	if err := s.Call(ctx, id, agentproto.MethodFrpsApply, agentproto.ApplyRequest{Rev: rev, Settings: &set}, &res, callTimeout); err != nil {
		return res, err
	}
	applied := s.appliedRevOf(id)
	if res.Applied {
		applied = res.Rev
	}
	if err := store.SetNodeApplied(ctx, s.st.DB(), id, applied, res.Error); err != nil {
		s.log.Error("nodes: set applied", "node", id, "err", err)
	}
	return res, nil
}

// ---- 存储租约 ----

// pushLease 给在线节点下发存储密钥和租约。force 为 false 时，leaseRenew 内推过的跳过。
func (s *Service) pushLease(ctx context.Context, id string, force bool) error {
	s.mu.Lock()
	c := s.conns[id]
	last := s.leased[id]
	s.mu.Unlock()
	if c == nil || !hasFeature(&c.hello, agentproto.FeatureSettings) || c.hello.MachineID == "" {
		return nil
	}
	if !force && s.now().Sub(last) < leaseRenew {
		return nil
	}
	master, err := s.credKey(ctx)
	if err != nil {
		return err
	}
	now := s.now()
	l := agentproto.StoreLease{
		Key:        base64.StdEncoding.EncodeToString(nodeticket.StoreKey(master, id, c.hello.MachineID)),
		LeaseUntil: now.Add(LeaseTTL).Unix(), ServerTime: now.Unix(),
	}
	if err := s.Call(ctx, id, agentproto.MethodStoreLease, l, nil, callTimeout); err != nil {
		return err
	}
	s.mu.Lock()
	s.leased[id] = now
	s.mu.Unlock()
	return nil
}

// ---- 面板主密钥 ----

// SecretInfo 是面板主密钥的概况（不含密钥本身）。
type SecretInfo struct {
	Fingerprint string     `json:"fingerprint"`        // 例如 K7QF-2M9X-AB3D-PQ7Z，看换没换
	Previous    string     `json:"previous,omitempty"` // 轮换时：换之前的指纹
	Version     int        `json:"version"`            // 第几版，每轮换一次加一
	RotatedAt   *time.Time `json:"rotatedAt,omitempty"`
	Protection  string     `json:"protection"`      // 数据库里怎么保存的，例如"AES-256-GCM 加密，密钥在 /data/secret.key"
	Error       string     `json:"error,omitempty"` // 主密钥解不开时的原因（只能轮换）
}

// Secret 返回面板主密钥的概况。
func (s *Service) Secret(ctx context.Context) (SecretInfo, error) {
	out := SecretInfo{Version: 1, Protection: "AES-256-GCM 加密保存，加密密钥在 " + s.kekSource + "（不在数据库里）"}
	if v, ok, _ := store.GetSetting(ctx, s.st.DB(), settingKeyVersion); ok {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			out.Version = n
		}
	}
	if v, ok, _ := store.GetSetting(ctx, s.st.DB(), settingKeyRotatedAt); ok {
		if sec, err := strconv.ParseInt(v, 10, 64); err == nil {
			t := time.Unix(sec, 0).UTC()
			out.RotatedAt = &t
		}
	}
	key, err := s.credKey(ctx)
	if errors.Is(err, ErrKeyUnreadable) {
		out.Error = err.Error()
		return out, nil
	}
	if err != nil {
		return SecretInfo{}, err
	}
	out.Fingerprint = fingerprint(key)
	return out, nil
}

// RotateSecret 换一个新的面板主密钥：所有已签发的登录票据、节点的票据密钥和存储密钥立即作废，
// 新的授权表和存储租约马上推给在线节点（节点用新密钥重新加密本地存储）；在线客户端通过实时通道
// 马上丢掉旧票据、领新票据重连。
func (s *Service) RotateSecret(ctx context.Context, by Actor) (SecretInfo, error) {
	prev, _ := s.Secret(ctx)
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return SecretInfo{}, err
	}
	sealed, err := sealKey(s.kek, b)
	if err != nil {
		return SecretInfo{}, err
	}
	err = s.st.Tx(ctx, func(tx *sql.Tx) error {
		if err := store.SetSetting(ctx, tx, settingCredKey, sealed); err != nil {
			return err
		}
		if err := store.SetSetting(ctx, tx, settingKeyRotatedAt, strconv.FormatInt(s.now().Unix(), 10)); err != nil {
			return err
		}
		if err := store.SetSetting(ctx, tx, settingKeyVersion, strconv.Itoa(prev.Version+1)); err != nil {
			return err
		}
		return s.audit(ctx, tx, by, store.AuditNodeSecret, "", map[string]any{"from": prev.Fingerprint, "to": fingerprint(b), "version": prev.Version + 1})
	})
	if err != nil {
		return SecretInfo{}, err
	}
	s.keyMu.Lock()
	s.key = b
	s.keyMu.Unlock()
	s.mu.Lock()
	ids := make([]string, 0, len(s.conns))
	for id := range s.conns {
		ids = append(ids, id)
	}
	s.mu.Unlock()
	for _, id := range ids {
		if err := s.pushLease(ctx, id, true); err != nil {
			s.log.Warn("push store lease failed", "node", id, "err", err)
		}
	}
	s.SyncAllAccess(ctx, true)
	s.ticketsInvalid("")
	out, err := s.Secret(ctx)
	out.Previous = prev.Fingerprint
	return out, err
}

// ---- 旧配置转换 ----

// convertLegacy 把还没有结构化配置的节点的 TOML 转换过来（升级到托管节点时执行一次）。
// 认识的字段照搬，token 丢掉（节点不再用 token），其余用默认值。
func (s *Service) convertLegacy(ctx context.Context) {
	ns, err := store.ListNodes(ctx, s.st.DB())
	if err != nil {
		s.log.Error("nodes: list for conversion", "err", err)
		return
	}
	for _, n := range ns {
		if n.Settings != "" {
			continue
		}
		set := fromTOML(n.FrpsConfig)
		if err := set.Validate(); err != nil {
			s.log.Warn("nodes: converted frps config is invalid, using defaults", "node", n.ID, "err", err)
			port := set.BindPort
			set = frpsset.Defaults()
			if port > 0 {
				set.BindPort = port
			}
		}
		b, _ := json.Marshal(set)
		if _, err := store.SetNodeSettings(ctx, s.st.DB(), n.ID, string(b), s.now().Unix()); err != nil {
			s.log.Error("nodes: save converted settings", "node", n.ID, "err", err)
			continue
		}
		s.log.Info("nodes: converted frps TOML to settings", "node", n.ID)
	}
}

// fromTOML 从旧的 frps TOML 里取出认识的字段。
func fromTOML(cfg string) frpsset.Settings {
	set := frpsset.Defaults()
	if strings.TrimSpace(cfg) == "" {
		return set
	}
	var c struct {
		BindAddr               string `toml:"bindAddr"`
		BindPort               int    `toml:"bindPort"`
		KCPBindPort            int    `toml:"kcpBindPort"`
		QUICBindPort           int    `toml:"quicBindPort"`
		VhostHTTPPort          int    `toml:"vhostHTTPPort"`
		VhostHTTPSPort         int    `toml:"vhostHTTPSPort"`
		TCPMuxHTTPConnectPort  int    `toml:"tcpmuxHTTPConnectPort"`
		SubDomainHost          string `toml:"subDomainHost"`
		VhostHTTPTimeout       int    `toml:"vhostHTTPTimeout"`
		MaxPortsPerClient      int    `toml:"maxPortsPerClient"`
		UserConnTimeout        int    `toml:"userConnTimeout"`
		UDPPacketSize          int    `toml:"udpPacketSize"`
		NatHoleReserveHours    int    `toml:"natholeAnalysisDataReserveHours"`
		DetailedErrorsToClient *bool  `toml:"detailedErrorsToClient"`
		AllowPorts             []struct {
			Start, End, Single int
		} `toml:"allowPorts"`
		Log struct {
			Level string `toml:"level"`
		} `toml:"log"`
		Transport struct {
			TCPMux           *bool `toml:"tcpMux"`
			MaxPoolCount     int   `toml:"maxPoolCount"`
			HeartbeatTimeout int   `toml:"heartbeatTimeout"`
			TLS              struct {
				Force bool `toml:"force"`
			} `toml:"tls"`
		} `toml:"transport"`
	}
	if toml.Unmarshal([]byte(cfg), &c) != nil {
		return set
	}
	if c.BindPort > 0 {
		set.BindPort = c.BindPort
	}
	set.BindAddr, set.KCPBindPort, set.QUICBindPort = c.BindAddr, c.KCPBindPort, c.QUICBindPort
	set.VhostHTTPPort, set.VhostHTTPSPort, set.TCPMuxHTTPConnectPort = c.VhostHTTPPort, c.VhostHTTPSPort, c.TCPMuxHTTPConnectPort
	set.SubDomainHost, set.VhostHTTPTimeout, set.MaxPortsPerClient = c.SubDomainHost, c.VhostHTTPTimeout, c.MaxPortsPerClient
	set.UserConnTimeout, set.UDPPacketSize, set.NatHoleReserveHours = c.UserConnTimeout, c.UDPPacketSize, c.NatHoleReserveHours
	set.MaxPoolCount, set.HeartbeatTimeout, set.TLSForce = c.Transport.MaxPoolCount, c.Transport.HeartbeatTimeout, c.Transport.TLS.Force
	if c.DetailedErrorsToClient != nil {
		set.DetailedErrorsToClient = *c.DetailedErrorsToClient
	}
	if c.Transport.TCPMux != nil {
		set.TCPMux = *c.Transport.TCPMux
	}
	if c.Log.Level != "" {
		set.LogLevel = c.Log.Level
	}
	if c.AllowPorts != nil {
		var r quota.Ranges
		for _, p := range c.AllowPorts {
			if p.Single > 0 {
				r = append(r, quota.Range{Lo: p.Single, Hi: p.Single})
			} else if p.Start > 0 {
				r = append(r, quota.Range{Lo: p.Start, Hi: max(p.End, p.Start)})
			}
		}
		set.AllowPorts = r.String()
	}
	return set
}

// openToUsers 判断节点是否开放给普通用户（管理员总能用）。
func openToUsers(roles []string) bool { return slices.Contains(roles, "user") }
