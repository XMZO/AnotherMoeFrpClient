// Package legacy 处理旧版 PySide6 客户端的请求：切换后只给出"请升级"提示（PLAN 7.12）。
//
// 旧客户端登录分两步：先 POST /api/login/get_challenge 拿挑战码，再 POST /api/login。
// 第一步失败时它只会显示写死的"获取挑战码失败"，所以第一步要返回 200 和一个假挑战码，
// 让它走到第二步；第二步以及其他所有旧路径返回 426，旧客户端会把 error 字段原样弹出来。
//
// 旧版 import-legacy（导入 users.db）属于 Phase 6，不在这里。
package legacy

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"

	"moefrp/shared/api"
)

// Handler 返回旧路径的处理器。downloadURL 会拼进提示文字里。
func Handler(downloadURL string) http.Handler {
	msg := "请下载新版客户端"
	if downloadURL != "" {
		msg += "：" + downloadURL
	}
	upgrade, _ := json.Marshal(api.LegacyErrorResponse{Error: msg})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Type", "application/json; charset=utf-8")
		h.Set("Cache-Control", "no-store")
		if r.Method == http.MethodPost && r.URL.Path == api.LegacyChallenge {
			// 与旧服务端一样是 32 字节的十六进制串，内容没有任何意义
			var b [32]byte
			_, _ = rand.Read(b[:])
			resp, _ := json.Marshal(api.LegacyChallengeResponse{Challenge: hex.EncodeToString(b[:])})
			w.WriteHeader(http.StatusOK)
			w.Write(resp)
			return
		}
		w.WriteHeader(http.StatusUpgradeRequired)
		w.Write(upgrade)
	})
}
