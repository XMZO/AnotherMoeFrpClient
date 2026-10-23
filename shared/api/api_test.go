package api

import (
	"encoding/json"
	"strings"
	"testing"

	"moefrp/shared/model"
)

func TestRoutes(t *testing.T) {
	for _, r := range []string{
		RouteClientConfig, RouteRegister, RouteLogin, RouteLogout, RouteResetPassword, RouteChangePassword,
		RouteMe, RouteState, RouteProfiles, RouteProfile, RouteShares, RouteShare, RouteShareRepublish,
		RouteSharePreview, RouteSubscriptions, RouteSubscription, RouteSubscriptionRuntime,
	} {
		if !strings.HasPrefix(r, Prefix+"/") {
			t.Errorf("%s 不在 %s 下", r, Prefix)
		}
	}
	if got := ProfilePath("abc"); got != "/api/v1/profiles/abc" {
		t.Errorf("ProfilePath = %s", got)
	}
	if got := ProfilePath("a/b"); got != "/api/v1/profiles/a%2Fb" {
		t.Errorf("ID 应被转义：%s", got)
	}
	if got := SubscriptionRuntimePath("s1"); got != "/api/v1/subscriptions/s1/runtime" {
		t.Errorf("SubscriptionRuntimePath = %s", got)
	}
	if got := SharePreviewPath("x"); got != "/api/v1/shares/x/preview" {
		t.Errorf("SharePreviewPath = %s", got)
	}
	if StateETag(0) != `"0"` || StateETag(42) != `"42"` {
		t.Error("StateETag 格式不对")
	}
}

func TestVersionCompare(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"3.0.0", "3.0.0", 0},
		{"v3.0.0", "3.0.0", 0},
		{"3.0", "3.0.0", 0},
		{"3", "3.0.0", 0},
		{"3.0.1", "3.0.0", 1},
		{"3.1.0", "3.0.9", 1},
		{"10.0.0", "9.9.9", 1},
		{"2.9.9", "3.0.0", -1},
		{"3.0.0-beta.1", "3.0.0", -1},
		{"3.0.0-beta.2", "3.0.0-beta.10", -1},
		{"3.0.0-alpha", "3.0.0-beta", -1},
		{"3.0.0-1", "3.0.0-alpha", -1},
		{"3.0.0-beta", "3.0.0-beta.1", -1},
		{"3.0.0+build.5", "3.0.0", 0},
	} {
		got, err := CompareVersions(tc.a, tc.b)
		if err != nil {
			t.Errorf("%s vs %s: %v", tc.a, tc.b, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s vs %s = %d, want %d", tc.a, tc.b, got, tc.want)
		}
		if back, _ := CompareVersions(tc.b, tc.a); back != -tc.want {
			t.Errorf("%s vs %s 反向比较不对称", tc.b, tc.a)
		}
	}
	for _, bad := range []string{"", "dev", "1.2.3.4", "1..2", "1.x", "1.2.3-", "1.2.3-a..b", "-1.0.0"} {
		if _, err := ParseVersion(bad); err == nil {
			t.Errorf("%q 应解析失败", bad)
		}
	}
}

// 请求结构体按 PLAN 5.1 使用 camelCase，客户端和服务端用的是同一份定义。
func TestRequestJSON(t *testing.T) {
	b, _ := json.Marshal(RegisterRequest{Nickname: "a", Password: "b", InviteCode: "c"})
	if string(b) != `{"nickname":"a","password":"b","inviteCode":"c"}` {
		t.Errorf("RegisterRequest: %s", b)
	}
	b, _ = json.Marshal(ChangePasswordRequest{OldPassword: "a", NewPassword: "b"})
	if string(b) != `{"oldPassword":"a","newPassword":"b"}` {
		t.Errorf("ChangePasswordRequest: %s", b)
	}
	b, _ = json.Marshal(ResetPasswordRequest{Nickname: "a", Code: "b", NewPassword: "c"})
	if string(b) != `{"nickname":"a","code":"b","newPassword":"c"}` {
		t.Errorf("ResetPasswordRequest: %s", b)
	}

	req := UpdateProfileRequest{BaseRevision: 7, Name: "p", Nodes: []model.Node{{ID: "n"}}}
	b, _ = json.Marshal(req)
	var back UpdateProfileRequest
	if err := json.Unmarshal(b, &back); err != nil || back.BaseRevision != 7 || back.Data().Nodes[0].ID != "n" {
		t.Errorf("UpdateProfileRequest 往返失败：%s %v", b, err)
	}
	if !strings.Contains(string(b), `"baseRevision":7`) {
		t.Errorf("缺少 baseRevision：%s", b)
	}

	b, _ = json.Marshal(ClientConfigResponse{ImageSources: []ImageSource{{URL: "u", Weight: 5, IsAPI: true, JSONPath: "data.url"}}})
	for _, want := range []string{`"latestVersion"`, `"minVersion"`, `"downloadUrl"`, `"sha256"`, `"announcement"`, `"isApi":true`, `"jsonPath":"data.url"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("ClientConfigResponse 缺少 %s：%s", want, b)
		}
	}
}
