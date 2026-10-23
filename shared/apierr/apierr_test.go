package apierr

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"
)

// PLAN 5.4 的错误码与 HTTP 状态码对照表。
func TestStatusTable(t *testing.T) {
	want := map[Code]int{
		Unauthorized:       401,
		SessionExpired:     401,
		SessionReplaced:    401,
		Forbidden:          403,
		NotFound:           404,
		ValidationFailed:   400,
		InvalidCredentials: 401,
		AccountLocked:      429,
		AccountDisabled:    403,
		InviteInvalid:      400,
		InviteUsed:         409,
		NicknameTaken:      409,
		ResetCodeInvalid:   400,
		RevisionConflict:   409,
		ShareRevoked:       410,
		ShareExpired:       410,
		ShareFull:          409,
		AlreadySubscribed:  409,
		LimitExceeded:      409,
		RateLimited:        429,
		ClientOutdated:     426,
		Internal:           500,
		MethodNotAllowed:   405,
		PayloadTooLarge:    413,
	}
	for code, status := range want {
		if got := code.Status(); got != status {
			t.Errorf("%s: status = %d, want %d", code, got, status)
		}
		if !code.Known() || code.DefaultMessage() == "" {
			t.Errorf("%s: 缺少默认说明", code)
		}
	}
	if len(Codes()) != len(want) {
		t.Errorf("定义了 %d 个错误码，表里有 %d 个", len(Codes()), len(want))
	}
	if Code("nope").Status() != http.StatusInternalServerError || Code("nope").Known() {
		t.Error("未知错误码应按 500 处理")
	}
}

func TestJSONShape(t *testing.T) {
	e := New(RevisionConflict).With(DetailCurrent, map[string]any{"revision": 4})
	b, err := json.Marshal(Response{Error: e})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"error":{"code":"revision_conflict","message":"配置已在其他设备修改","details":{"current":{"revision":4}}}}`
	if string(b) != want {
		t.Fatalf("got  %s\nwant %s", b, want)
	}

	b, _ = json.Marshal(Response{Error: New(NotFound)})
	if string(b) != `{"error":{"code":"not_found","message":"资源不存在"}}` {
		t.Fatalf("没有 details 时应省略：%s", b)
	}

	var back Response
	if err := json.Unmarshal([]byte(want), &back); err != nil || back.Error.Code != RevisionConflict {
		t.Fatalf("反序列化失败：%v %+v", err, back)
	}
}

func TestErrorsIsAs(t *testing.T) {
	err := fmt.Errorf("wrap: %w", Newf(InviteUsed, "邀请码 %s 已被使用", "FRPT-AAAA-AAAA"))
	if !errors.Is(err, New(InviteUsed)) {
		t.Fatal("errors.Is 应按错误码匹配")
	}
	if errors.Is(err, New(InviteInvalid)) {
		t.Fatal("不同错误码不应匹配")
	}
	e, ok := As(err)
	if !ok || e.Code != InviteUsed || e.Status() != 409 {
		t.Fatalf("As 失败：%v", e)
	}
	if CodeOf(err) != InviteUsed || CodeOf(errors.New("x")) != Internal {
		t.Fatal("CodeOf 不对")
	}
	if New(NotFound).Error() != "not_found" {
		t.Fatalf("Error() = %q", New(NotFound).Error())
	}
}
