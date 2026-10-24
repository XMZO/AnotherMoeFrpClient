package nodes

import (
	"testing"

	"moefrp/shared/agentproto"
)

// 网络速率：用 agent 的采样时间差算（不受上报到达时间抖动影响）；换了连接重新开始；
// 旧版 agent 没有采样时间时，到达间隔太短的跳过，不算出尖峰。
func TestNetRate(t *testing.T) {
	s, _ := newSecretService(t, make([]byte, 32))
	c1, c2 := &agentConn{}, &agentConn{}
	rate := func() (float64, float64) { l := s.live["n1"]; return l.rxRate, l.txRate }

	s.recordReport("n1", c1, agentproto.Report{NetRx: 1000, NetTx: 1000, At: 10_000})
	// 隔 3 秒采样，但两份几乎同时到达面板
	s.recordReport("n1", c1, agentproto.Report{NetRx: 4000, NetTx: 7000, At: 13_000})
	if rx, tx := rate(); rx != 1000 || tx != 2000 {
		t.Fatalf("按采样时间：%v %v", rx, tx)
	}
	// 换了连接（重连、升级）：不拿旧的当基准
	s.recordReport("n1", c2, agentproto.Report{NetRx: 900_000_000, NetTx: 900_000_000, At: 14_000})
	if rx, tx := rate(); rx != 0 || tx != 0 {
		t.Fatalf("新连接应归零：%v %v", rx, tx)
	}
	// 旧版 agent（没有采样时间），到达间隔太短：保留上一次的值
	s.recordReport("n1", c2, agentproto.Report{NetRx: 900_300_000, NetTx: 900_300_000})
	if rx, _ := rate(); rx != 0 {
		t.Fatalf("间隔太短应跳过：%v", rx)
	}
}
