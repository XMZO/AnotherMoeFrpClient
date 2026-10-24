package frpsrv

import (
	"reflect"
	"unsafe"

	"github.com/fatedier/frp/server"
)

// 按 run ID 关闭 frps 上的某个客户端（frpc）：准入插件发现它的登录已经失效（会话被吊销、没有权限了、
// 凭证被重置、面板换了密钥、同一会话在别处重新连接）时用，连同它的代理和正在传的连接一起断开。
//
// frps 有 ControlManager.GetByID 和 Control.Close，但 ControlManager 挂在 Service 的未导出字段 ctlManager 上，
// 外部拿不到，所以这里用反射取。这依赖 frp 的内部结构：agent/internal/access 的 TestWithFrps 用真的 frps 和 frpc
// 盯着（吊销后连接必须被关掉），升级 frp 时结构变了测试会失败；拿不到时 CloseClient 返回 false，
// 退回到"不接受新代理和新连接"。

// controlManager 取 frps 的 ControlManager，拿不到时返回 nil。调用方持有 m.mu。
func (m *Manager) controlManagerLocked() *server.ControlManager {
	if m.svc == nil {
		return nil
	}
	f := reflect.ValueOf(m.svc).Elem().FieldByName("ctlManager")
	if !f.IsValid() || f.Type() != reflect.TypeOf((*server.ControlManager)(nil)) || f.IsNil() {
		return nil
	}
	return (*server.ControlManager)(unsafe.Pointer(f.Pointer()))
}

// CloseClient 关闭 run ID 对应的 frpc 连接，返回是否找到并关闭了。
func (m *Manager) CloseClient(runID string) bool {
	m.mu.Lock()
	cm := m.controlManagerLocked()
	m.mu.Unlock()
	if cm == nil || runID == "" {
		return false
	}
	ctl, ok := cm.GetByID(runID)
	if !ok {
		return false
	}
	_ = ctl.Close()
	return true
}

// HasClient 判断 run ID 对应的 frpc 还连着。拿不到 frps 内部状态时按"还连着"处理。
func (m *Manager) HasClient(runID string) bool {
	m.mu.Lock()
	cm := m.controlManagerLocked()
	m.mu.Unlock()
	if cm == nil {
		return true
	}
	_, ok := cm.GetByID(runID)
	return ok
}
