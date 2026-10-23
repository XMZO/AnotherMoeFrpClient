package main

import (
	"os"
	"path/filepath"
	"runtime"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// 从 spike/app-engine/webview2check.go 搬过来（PLAN 6.11）。

// Evergreen WebView2 Runtime 的 EdgeUpdate 客户端 ID（与 Wails NSIS 模板里的检查一致）
const webview2ClientKey = `Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}`

const webview2DownloadURL = "https://go.microsoft.com/fwlink/p/?LinkId=2124703"

// webview2Version 在 app.Run() 之前检查 WebView2 是否可用。
// Wails v3 beta.31 缺 WebView2 时只往 stderr 打日志并 os.Exit(1)，Options.ErrorHandler 收不到，所以得自己先查。
func webview2Version(browserPath string) (string, bool) {
	if browserPath != "" {
		// 固定版本运行时：目录里要有 EBWebView\<arch>\EmbeddedBrowserWebView.dll
		arch := map[string]string{"amd64": "x64", "arm64": "arm64", "386": "x86"}[runtime.GOARCH]
		if _, err := os.Stat(filepath.Join(browserPath, "EBWebView", arch, "EmbeddedBrowserWebView.dll")); err == nil {
			return "fixed:" + browserPath, true
		}
		return "", false
	}
	for _, k := range []struct {
		root registry.Key
		path string
	}{
		{registry.LOCAL_MACHINE, `SOFTWARE\WOW6432Node\` + webview2ClientKey},
		{registry.LOCAL_MACHINE, `SOFTWARE\` + webview2ClientKey},
		{registry.CURRENT_USER, `Software\` + webview2ClientKey},
	} {
		key, err := registry.OpenKey(k.root, k.path, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		pv, _, err := key.GetStringValue("pv")
		key.Close()
		if err == nil && pv != "" && pv != "0.0.0.0" {
			return pv, true
		}
	}
	return "", false
}

// requireWebView2 缺少 WebView2 时弹框说明原因并给出下载地址，然后退出。
func requireWebView2(browserPath string) {
	v, ok := webview2Version(browserPath)
	if ok {
		diag("webview2 precheck: %s", v)
		return
	}
	diag("webview2 precheck: missing")
	windows.MessageBox(0,
		windows.StringToUTF16Ptr("没有检测到 Microsoft Edge WebView2 Runtime，程序无法显示界面。\n\n请先安装："+webview2DownloadURL),
		windows.StringToUTF16Ptr(productName), windows.MB_ICONERROR|windows.MB_OK)
	os.Exit(1)
}

// systemUsesDarkTheme 读系统"应用模式"，用来决定窗口底色，避免启动时白屏或黑屏闪一下。
func systemUsesDarkTheme() bool {
	key, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer key.Close()
	v, _, err := key.GetIntegerValue("AppsUseLightTheme")
	return err == nil && v == 0
}
