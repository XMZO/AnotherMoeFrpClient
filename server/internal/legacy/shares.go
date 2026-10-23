package legacy

// 旧版 shares / subscriptions 的导入（PLAN 7.12、附录 C）。
//
// 类型判定：
//   - is_template=1 → template（快照只有节点）
//   - 否则，代理全是 stcp/xtcp，并且有订阅者带 visitors 参数 → p2p（旧版访客走第一个节点）
//   - 其余 → full
//
// 分享 ID 沿用旧值（"share-<uuid>"），用户手里保存的旧分享 ID 仍然可以订阅。
// 旧分享没有来源配置，导入后不能"重新发布"。

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"html"
	"strings"

	"moefrp/server/internal/store"
	"moefrp/shared/model"
	"moefrp/shared/validate"
)

type legacyShare struct {
	ID, Owner, Name string
	IsTemplate      bool
	JSON            string
}

type legacySubscription struct {
	ID, User, ShareID string
	Params            sql.NullString
}

type legacyVisitor struct {
	Name       flexString `json:"name"`
	Type       flexString `json:"type"`
	ServerName flexString `json:"serverName"`
	SecretKey  flexString `json:"secretKey"`
	BindAddr   flexString `json:"bindAddr"`
	BindPort   flexString `json:"bindPort"`
}

type legacyParams struct {
	Proxies    []legacyProxy   `json:"proxies"`
	NodeRemark flexString      `json:"node_remark"`
	Visitors   []legacyVisitor `json:"visitors"`
}

func readShares(ctx context.Context, db *sql.DB) ([]legacyShare, error) {
	if n, err := countRows(ctx, db, "shares"); err != nil || n == 0 {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT share_id, owner_uuid, share_name, COALESCE(is_template, 0), config_data_json FROM shares ORDER BY rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []legacyShare
	for rows.Next() {
		var s legacyShare
		if err := rows.Scan(&s.ID, &s.Owner, &s.Name, &s.IsTemplate, &s.JSON); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func readSubscriptions(ctx context.Context, db *sql.DB) ([]legacySubscription, error) {
	if n, err := countRows(ctx, db, "subscriptions"); err != nil || n == 0 {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT subscription_id, user_uuid, share_id, user_params_json FROM subscriptions ORDER BY rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []legacySubscription
	for rows.Next() {
		var s legacySubscription
		if err := rows.Scan(&s.ID, &s.User, &s.ShareID, &s.Params); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func parseParams(s sql.NullString) (legacyParams, error) {
	var p legacyParams
	raw := strings.TrimSpace(s.String)
	if !s.Valid || raw == "" || raw == "null" {
		return p, nil
	}
	err := json.Unmarshal([]byte(raw), &p)
	return p, err
}

// importedShare 记录导入后的分享，供订阅导入使用。
type importedShare struct {
	Kind    string
	Payload model.SharePayload
	Subs    int
}

func importShares(ctx context.Context, tx *sql.Tx, rep *Report, shares []legacyShare, subs []legacySubscription, ts int64, imported map[string]bool) (map[string]*importedShare, error) {
	// 先看哪些分享有带 visitors 的订阅
	hasVisitors := map[string]bool{}
	for _, s := range subs {
		if p, err := parseParams(s.Params); err == nil && len(p.Visitors) > 0 {
			hasVisitors[s.ShareID] = true
		}
	}

	out := map[string]*importedShare{}
	perUser := map[string]int{}
	for _, sh := range shares {
		name := strings.TrimSpace(html.UnescapeString(sh.Name))
		key := fmt.Sprintf("%s（%s）", sh.ID, name)
		if !imported[sh.Owner] {
			rep.problem("shares", key, true, "所有者 %s 没有导入", sh.Owner)
			continue
		}
		if validate.ID("id", sh.ID) != nil {
			rep.problem("shares", key, true, "分享 ID 不合法")
			continue
		}
		if perUser[sh.Owner] >= validate.MaxSharesPerUser {
			rep.problem("shares", key, true, "该用户的分享超过 %d 个", validate.MaxSharesPerUser)
			continue
		}
		if validate.ShareName(name) != nil {
			if name == "" {
				name = "导入的分享"
			} else {
				name = truncateRunes(name, validate.MaxShareNameLen)
			}
			rep.problem("shares", key, false, "分享名不合法，已改为 %q", name)
		}
		data, err := convertConfigJSON(rep, key, sh.JSON)
		if err != nil {
			rep.problem("shares", key, true, "config_data_json 无法解析：%v", err)
			continue
		}

		kind := model.ShareFull
		payload := model.SharePayload{Nodes: data.Nodes}
		switch {
		case sh.IsTemplate:
			kind = model.ShareTemplate
			if len(data.Proxies) > 0 {
				rep.problem("shares", key, false, "模板分享里的 %d 个代理已忽略", len(data.Proxies))
			}
		case allP2P(data.Proxies) && hasVisitors[sh.ID]:
			kind = model.ShareP2P
			if len(payload.Nodes) > 1 {
				payload.Nodes = payload.Nodes[:1] // 旧版访客总是走第一个节点
			}
			for _, p := range data.Proxies {
				payload.Targets = append(payload.Targets, model.P2PTarget{
					ID: p.ID, ServerName: p.Name, Type: p.Type, SecretKey: p.SecretKey, LocalPort: p.LocalPort,
				})
			}
		default:
			payload.Proxies = data.Proxies
		}
		payload.Normalize()
		if err := validate.SharePayload(kind, payload); err != nil {
			rep.problem("shares", key, true, "转换后仍不合法：%v", err)
			continue
		}
		blob, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		row := store.ShareRow{
			ID: sh.ID, OwnerID: sh.Owner, Name: name, Kind: kind, Payload: string(blob),
			Revision: 1, CreatedAt: ts, UpdatedAt: ts,
		}
		if err := store.InsertShare(ctx, tx, row); err != nil {
			if store.IsUniqueViolation(err) {
				rep.problem("shares", key, true, "分享 ID 重复")
				continue
			}
			return nil, err
		}
		if _, err := store.BumpStateVersion(ctx, tx, sh.Owner); err != nil {
			return nil, err
		}
		perUser[sh.Owner]++
		out[sh.ID] = &importedShare{Kind: kind, Payload: payload}
		rep.SharesImported++
	}
	return out, nil
}

func allP2P(proxies []model.Proxy) bool {
	if len(proxies) == 0 {
		return false
	}
	for _, p := range proxies {
		if p.Type != model.ProxySTCP && p.Type != model.ProxyXTCP {
			return false
		}
	}
	return true
}

func importSubscriptions(ctx context.Context, tx *sql.Tx, rep *Report, subs []legacySubscription, ts int64, imported map[string]bool, shares map[string]*importedShare) error {
	perUser := map[string]int{}
	seen := map[string]bool{}
	for _, s := range subs {
		key := s.ID
		if !imported[s.User] {
			rep.problem("subscriptions", key, true, "订阅者 %s 没有导入", s.User)
			continue
		}
		sh, ok := shares[s.ShareID]
		if !ok {
			rep.problem("subscriptions", key, true, "分享 %s 没有导入", s.ShareID)
			continue
		}
		pair := s.User + "\x00" + s.ShareID
		if seen[pair] {
			rep.problem("subscriptions", key, true, "同一用户重复订阅了分享 %s", s.ShareID)
			continue
		}
		if perUser[s.User] >= validate.MaxSubscriptionsPerUser {
			rep.problem("subscriptions", key, true, "该用户的订阅超过 %d 个", validate.MaxSubscriptionsPerUser)
			continue
		}
		lp, err := parseParams(s.Params)
		if err != nil {
			rep.problem("subscriptions", key, true, "user_params_json 无法解析：%v", err)
			continue
		}
		params := convertParams(rep, key, sh, lp)
		params.Normalize()
		if err := validate.SubscriptionParams(sh.Kind, sh.Payload, params); err != nil {
			rep.problem("subscriptions", key, true, "转换后仍不合法：%v", err)
			continue
		}
		blob, err := json.Marshal(params)
		if err != nil {
			return err
		}
		id := s.ID
		if validate.ID("id", id) != nil {
			id = model.NewID()
		}
		err = store.InsertSubscription(ctx, tx, store.SubscriptionRow{
			ID: id, UserID: s.User, ShareID: s.ShareID, Params: string(blob), Revision: 1, CreatedAt: ts, UpdatedAt: ts,
		})
		if err != nil {
			if store.IsUniqueViolation(err) {
				rep.problem("subscriptions", key, true, "订阅 ID 重复")
				continue
			}
			return err
		}
		if _, err := store.BumpStateVersion(ctx, tx, s.User); err != nil {
			return err
		}
		seen[pair] = true
		perUser[s.User]++
		sh.Subs++
		rep.SubscriptionsImported++
	}
	return nil
}

func convertParams(rep *Report, key string, sh *importedShare, lp legacyParams) model.SubscriptionParams {
	var p model.SubscriptionParams
	switch sh.Kind {
	case model.ShareTemplate:
		remark := lp.NodeRemark.str()
		for _, n := range sh.Payload.Nodes {
			if n.Remark == remark {
				p.NodeID = n.ID
				break
			}
		}
		if p.NodeID == "" && len(sh.Payload.Nodes) > 0 {
			p.NodeID = sh.Payload.Nodes[0].ID
			if remark != "" {
				rep.problem("subscriptions", key, false, "找不到备注为 %q 的节点，已改用第一个节点", remark)
			}
		}
		names := map[string]bool{}
		for i, lpx := range lp.Proxies {
			px, ok := convertProxy(rep, key, i, lpx)
			if !ok || names[px.Name] {
				continue
			}
			if validate.ProfileData(model.ProfileData{Proxies: []model.Proxy{px}}) != nil {
				rep.problem("subscriptions", key, false, "代理 %q 不合法，已丢弃", px.Name)
				continue
			}
			names[px.Name] = true
			p.Proxies = append(p.Proxies, px)
		}
	case model.ShareP2P:
		byName := map[string]model.P2PTarget{}
		for _, t := range sh.Payload.Targets {
			byName[t.ServerName] = t
		}
		used := map[string]bool{}
		for _, v := range lp.Visitors {
			t, ok := byName[v.ServerName.str()]
			if !ok || used[t.ID] {
				rep.problem("subscriptions", key, false, "访客 %q 对应的目标 %q 不在分享里，已丢弃", v.Name.str(), v.ServerName.str())
				continue
			}
			port, ok := v.BindPort.port()
			if !ok || port == 0 {
				rep.problem("subscriptions", key, false, "访客 %q 的本地端口不合法，已丢弃", v.Name.str())
				continue
			}
			name := v.Name.str()
			if validate.ProfileData(model.ProfileData{Visitors: []model.Visitor{{ID: "x", Name: name, Type: t.Type, ServerName: t.ServerName, SecretKey: "x", BindPort: port}}}) != nil {
				name = "p2p-" + t.ServerName
			}
			used[t.ID] = true
			p.Binds = append(p.Binds, model.P2PBind{
				ID: model.NewID(), TargetID: t.ID, Name: name, Enabled: true,
				BindAddr: strings.TrimSpace(v.BindAddr.str()), BindPort: port,
			})
			if p.Binds[len(p.Binds)-1].BindAddr == "" {
				p.Binds[len(p.Binds)-1].BindAddr = "127.0.0.1"
			}
		}
	}
	return p
}
