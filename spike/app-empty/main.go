package main

import (
	"embed"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"golang.org/x/sys/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

// diag 把关键事件追加写到 exe 同目录的 spike-diag.log，供外部脚本判定托盘/单实例/关闭到托盘是否生效。
var (
	diagMu   sync.Mutex
	diagFile *os.File
)

func diag(format string, args ...any) {
	diagMu.Lock()
	defer diagMu.Unlock()
	if diagFile == nil {
		return
	}
	fmt.Fprintf(diagFile, "%s pid=%d %s\n", time.Now().Format("15:04:05.000"), os.Getpid(), fmt.Sprintf(format, args...))
}

func openDiag() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(filepath.Dir(exe), "spike-diag.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err == nil {
		diagFile = f
	}
}

// processStart 返回本进程的创建时间（内核记录），用来算"进程创建 → 前端首屏"的耗时。
func processStart() time.Time {
	var c, e, k, u windows.Filetime
	if err := windows.GetProcessTimes(windows.CurrentProcess(), &c, &e, &k, &u); err != nil {
		return time.Now()
	}
	return time.Unix(0, c.Nanoseconds())
}

// AppService 暴露给前端：前端挂载完成后调用 Ready，用来测启动耗时。
type AppService struct {
	start time.Time
}

func (s *AppService) Ready() string {
	d := time.Since(s.start)
	diag("frontend ready: %d ms since process creation", d.Milliseconds())
	return fmt.Sprintf("%d ms", d.Milliseconds())
}

func main() {
	openDiag()
	svc := &AppService{start: processStart()}
	diag("main() entered: %d ms since process creation, args=%v", time.Since(svc.start).Milliseconds(), os.Args[1:])

	var window *application.WebviewWindow
	quitting := false

	app := application.New(application.Options{
		Name:        "app-empty",
		Description: "MoeFrp Phase 0 spike: tray + single instance + close-to-tray",
		Services: []application.Service{
			application.NewService(svc),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Windows: application.WindowsOptions{
			// 窗口被隐藏到托盘后不能让应用自动退出
			DisableQuitOnLastWindowClosed: true,
			// 测试钩子：指向一个没有 WebView2 的目录，观察 Wails 默认的"缺少 WebView2"行为
			WebviewBrowserPath: os.Getenv("SPIKE_WEBVIEW_BROWSER_PATH"),
		},
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: "com.moefrp.spike.app-empty",
			OnSecondInstanceLaunch: func(data application.SecondInstanceData) {
				diag("second instance launched: args=%v", data.Args)
				if window != nil {
					window.Show()
					window.Restore()
					window.Focus()
					diag("window activated by second instance")
				}
			},
		},
		OnShutdown: func() {
			diag("OnShutdown")
		},
	})

	window = app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "MoeFrp Spike (empty)",
		Width:            900,
		Height:           600,
		BackgroundColour: application.NewRGB(6, 7, 15),
		URL:              "/",
	})

	// 点 X：取消关闭，改为隐藏到托盘
	window.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		if quitting {
			return
		}
		diag("WindowClosing intercepted -> hide to tray")
		window.Hide()
		e.Cancel()
	})

	tray := app.SystemTray.New()
	tray.SetTooltip("MoeFrp Spike (empty)")
	menu := app.NewMenu()
	menu.Add("显示主界面").OnClick(func(ctx *application.Context) {
		diag("tray menu: show")
		window.Show()
		window.Focus()
	})
	menu.Add("状态：空闲").SetEnabled(false)
	menu.AddSeparator()
	menu.Add("退出").OnClick(func(ctx *application.Context) {
		diag("tray menu: quit")
		quitting = true
		app.Quit()
	})
	tray.SetMenu(menu)
	tray.OnClick(func() {
		diag("tray left click -> show")
		window.Show()
		window.Focus()
	})

	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		diag("ApplicationStarted: %d ms since process creation", time.Since(svc.start).Milliseconds())
	})

	if err := app.Run(); err != nil {
		diag("app.Run error: %v", err)
		log.Fatal(err)
	}
	diag("app.Run returned")
}
