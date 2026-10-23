package services

import (
	"strings"

	"moefrp/client/internal/apperr"
	"moefrp/client/internal/autostart"
	"moefrp/client/internal/engine"
	"moefrp/client/internal/store"
)

// SettingsService：读写 settings.json 里用户可改的项。
type SettingsService struct {
	settings *store.SettingsStore
	sink     *engine.LogSink

	// 以下由 main 注入
	OnNetworkProxy func(mode, proxyURL string) // 网络代理改变后调用（切换 API 客户端和图片下载的代理）
	AutoStart      *autostart.Entry            // nil 表示不支持开机自启
}

func NewSettingsService(settings *store.SettingsStore, sink *engine.LogSink) *SettingsService {
	return &SettingsService{settings: settings, sink: sink}
}

func (s *SettingsService) Get() store.Settings {
	return s.settings.Get()
}

// Update 只更新用户可改的字段，其余字段（installId、上次的配置等）忽略。
// 开机自启先改注册表，成功后才保存设置。
func (s *SettingsService) Update(in store.Settings) (store.Settings, error) {
	before := s.settings.Get()
	in.ProxyURL = strings.TrimSpace(in.ProxyURL)
	if in.NetworkProxy == store.NetProxyCustom && !store.ValidProxyURL(in.ProxyURL) {
		return before, apperr.New(apperr.ProxyURLInvalid, in.ProxyURL)
	}
	if in.AutoStart != before.AutoStart {
		if s.AutoStart == nil {
			return before, apperr.New(apperr.AutoStartFailed, "not supported")
		}
		if err := s.AutoStart.Set(in.AutoStart); err != nil {
			return before, apperr.Wrap(apperr.AutoStartFailed, err)
		}
	}
	out, err := s.settings.Update(func(cur *store.Settings) error {
		cur.Theme, cur.CloseAction, cur.LogLevel, cur.AutoConnect = in.Theme, in.CloseAction, in.LogLevel, in.AutoConnect
		cur.NetworkProxy, cur.ProxyURL = in.NetworkProxy, in.ProxyURL
		cur.AutoStart, cur.ImagePanel, cur.ReleaseOnHide = in.AutoStart, in.ImagePanel, in.ReleaseOnHide
		return nil
	})
	if err != nil {
		if in.AutoStart != before.AutoStart && s.AutoStart != nil {
			_ = s.AutoStart.Set(before.AutoStart) // 设置没保存成功，注册表也改回去
		}
		return out, apperr.Wrap(apperr.InvalidArg, err)
	}
	if out.LogLevel != before.LogLevel {
		s.sink.InstallGlobal(out.LogLevel)
		s.sink.Appendf("info", "log level set to %s", out.LogLevel)
	}
	if (out.NetworkProxy != before.NetworkProxy || out.ProxyURL != before.ProxyURL) && s.OnNetworkProxy != nil {
		s.OnNetworkProxy(out.NetworkProxy, out.ProxyURL)
	}
	return out, nil
}

// ReconcileAutoStart 在启动时调用：设置里开着自启、但注册表里的路径不是当前 exe（例如换了位置或升级改了文件名）时重写。
func (s *SettingsService) ReconcileAutoStart() error {
	if s.AutoStart == nil || !s.settings.Get().AutoStart {
		return nil
	}
	if _, current, err := s.AutoStart.State(); err == nil && current {
		return nil
	}
	return s.AutoStart.Set(true)
}
