package vnet

import (
	"strings"
	"testing"

	"golang.zx2c4.com/wintun"
)

// 内置的 wintun.dll 能从内存加载（不需要管理员：只查驱动版本，没装驱动时报"没有运行"而不是加载失败）
func TestWintunLoadsFromMemory(t *testing.T) {
	_, err := wintun.RunningVersion()
	if err != nil && strings.Contains(err.Error(), "loading") {
		t.Fatalf("加载 wintun.dll 失败：%v", err)
	}
	t.Logf("RunningVersion err=%v", err)
}
