package nodes

import (
	"context"
	"encoding/json"

	"moefrp/server/internal/store"
	"moefrp/shared/agentproto"
	"moefrp/shared/api"
	"moefrp/shared/frpsset"
	"moefrp/shared/nodeticket"
)

// VnetRelays 返回能给联机房间中继的节点（在线、agent 支持、配置里开了联机中继端口、知道地址），
// 以及签中继票据用的节点密钥（节点 ID → 密钥）。
func (s *Service) VnetRelays(ctx context.Context) ([]api.VnetRelay, map[string][]byte, error) {
	d, err := s.loadAccess(ctx)
	if err != nil {
		return nil, nil, err
	}
	ns, err := store.ListNodes(ctx, s.st.DB())
	if err != nil {
		return nil, nil, err
	}
	out, keys := []api.VnetRelay{}, map[string][]byte{}
	for _, n := range ns {
		if !n.Enrolled || n.Settings == "" || len(d.key) == 0 {
			continue
		}
		var set frpsset.Settings
		if json.Unmarshal([]byte(n.Settings), &set) != nil || set.VnetRelayPort <= 0 {
			continue
		}
		s.mu.Lock()
		c := s.conns[n.ID]
		ok := c != nil && hasFeature(&c.hello, agentproto.FeatureVnetRelay)
		s.mu.Unlock()
		ci := clientInfo(n)
		if !ok || ci == nil || ci.Addr == "" {
			continue
		}
		out = append(out, api.VnetRelay{ID: n.ID, Name: n.Name, Region: n.Region, Addr: ci.Addr, Port: set.VnetRelayPort})
		keys[n.ID] = nodeticket.NodeKey(d.key, n.ID)
	}
	return out, keys, nil
}
