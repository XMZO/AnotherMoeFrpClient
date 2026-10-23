package main

import (
	"embed"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	golog "github.com/fatedier/golib/log"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"golang.org/x/sys/windows"

	frpversion "github.com/fatedier/frp/pkg/util/version"

	"spike/engine"
)

//go:embed all:frontend/dist
var assets embed.FS

var (
	diagMu   sync.Mutex
	diagFile *os.File
)

// diag 把关键事件追加写到 exe 同目录的 spike-diag.log，供外部脚本判定结果。
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

func processStart() time.Time {
	var c, e, k, u windows.Filetime
	if err := windows.GetProcessTimes(windows.CurrentProcess(), &c, &e, &k, &u); err != nil {
		return time.Now()
	}
	return time.Unix(0, c.Nanoseconds())
}

// AppService：前端挂载后调用 Ready 记录启动耗时；Info 返回版本信息。
type AppService struct{ start time.Time }

func (s *AppService) Ready() string {
	d := time.Since(s.start)
	diag("frontend ready: %d ms since process creation", d.Milliseconds())
	return fmt.Sprintf("%d ms", d.Milliseconds())
}

func (s *AppService) Info() map[string]string {
	return map[string]string{"frp": frpversion.Full(), "wails": "v3.0.0-beta.31"}
}

func main() {
	openDiag()
	appSvc := &AppService{start: processStart()}
	diag("main() entered: %d ms since process creation, args=%v", time.Since(appSvc.start).Milliseconds(), os.Args[1:])

	requireWebView2(os.Getenv("SPIKE_WEBVIEW_BROWSER_PATH"))

	// PLAN 6.3.6：进程启动时装一次 frp 全局日志出口
	sink := engine.NewLogSink(5000)
	sink.InstallGlobal(golog.InfoLevel)
	tunnel := NewTunnelService(sink)

	var window *application.WebviewWindow
	quitting := false

	winOpts := application.WindowsOptions{
		DisableQuitOnLastWindowClosed: true,
		// 默认是 %APPDATA%\<exe 名>；放到 LOCALAPPDATA 下更合适
		WebviewUserDataPath: filepath.Join(os.Getenv("LOCALAPPDATA"), "MoeFrpSpike", "WebView2"),
		// 测试钩子：指向一个没有 WebView2 的目录，模拟"缺少 WebView2"
		WebviewBrowserPath: os.Getenv("SPIKE_WEBVIEW_BROWSER_PATH"),
	}
	if p := os.Getenv("SPIKE_CDP_PORT"); p != "" {
		// 测试钩子：打开 WebView2 远程调试端口，让脚本通过 CDP 点击界面按钮
		winOpts.AdditionalBrowserArgs = append(winOpts.AdditionalBrowserArgs, "--remote-debugging-port="+p)
	}

	app := application.New(application.Options{
		Name:        "app-engine",
		Description: "MoeFrp Phase 0 spike: Wails v3 + embedded frp client",
		Services: []application.Service{
			application.NewService(appSvc),
			application.NewService(tunnel),
		},
		Assets:  application.AssetOptions{Handler: application.AssetFileServerFS(assets)},
		Windows: winOpts,
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: "com.moefrp.spike.app-engine",
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
		// 注意：beta.31 里缺 WebView2 的错误不会走到这里（见 REPORT），所以在 app.Run 前用 requireWebView2 预检
		ErrorHandler: func(err error) {
			diag("ErrorHandler: %v", err)
		},
		OnShutdown: func() {
			diag("OnShutdown: stopping engine")
			t0 := time.Now()
			err := tunnel.eng.Stop(500*time.Millisecond, 2*time.Second)
			diag("engine stopped in %d ms, err=%v", time.Since(t0).Milliseconds(), err)
		},
	})

	window = app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "MoeFrp Spike (engine)",
		Width:            960,
		Height:           640,
		BackgroundColour: application.NewRGB(6, 7, 15),
		URL:              "/",
	})
	window.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		if quitting {
			return
		}
		diag("WindowClosing intercepted -> hide to tray")
		window.Hide()
		e.Cancel()
	})

	tray := app.SystemTray.New()
	tray.SetTooltip("MoeFrp Spike (engine)")
	menu := app.NewMenu()
	menu.Add("显示主界面").OnClick(func(*application.Context) {
		window.Show()
		window.Focus()
	})
	stateItem := menu.Add("状态：idle").SetEnabled(false)
	toggleItem := menu.Add("启动隧道")
	toggleItem.OnClick(func(*application.Context) {
		if tunnel.eng.Snapshot().State == engine.StateIdle || tunnel.eng.Snapshot().State == engine.StateFailed {
			if err := tunnel.Start(); err != nil {
				diag("tray start error: %v", err)
			}
		} else {
			_ = tunnel.Stop()
		}
	})
	menu.AddSeparator()
	menu.Add("退出").OnClick(func(*application.Context) {
		diag("tray menu: quit")
		quitting = true
		app.Quit()
	})
	tray.SetMenu(menu)
	tray.OnClick(func() {
		window.Show()
		window.Focus()
	})
	tunnel.setOnState(func(v TunnelView) {
		stateItem.SetLabel("状态：" + string(v.Engine.State))
		if v.Engine.State == engine.StateIdle || v.Engine.State == engine.StateFailed {
			toggleItem.SetLabel("启动隧道")
		} else {
			toggleItem.SetLabel("停止隧道")
		}
		diag("tunnel state: %s runID=%s logins=%d proxy=%v err=%q", v.Engine.State, v.Engine.RunID, v.Engine.Logins, v.Proxy, v.Engine.LastError)
	})

	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		diag("ApplicationStarted: %d ms since process creation", time.Since(appSvc.start).Milliseconds())
		tunnel.run(app)
	})

	if err := app.Run(); err != nil {
		diag("app.Run error: %v", err)
		log.Fatal(err)
	}
	diag("app.Run returned")
}
