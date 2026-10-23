package main

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"golang.org/x/sys/windows"

	"moefrp/client/internal/apiclient"
	"moefrp/client/internal/autostart"
	"moefrp/client/internal/cloudsync"
	"moefrp/client/internal/credstore"
	"moefrp/client/internal/engine"
	"moefrp/client/internal/services"
	"moefrp/client/internal/store"
	"moefrp/client/internal/testhooks"
	"moefrp/shared/api"
	"moefrp/shared/apierr"
)

//go:embed all:frontend/dist
var assets embed.FS

// version 由构建时的 -ldflags "-X main.version=..." 注入（见 Taskfile 的 BUILD_FLAGS）。
var version = "dev"

// apiBase 是服务端地址，可以用 -ldflags "-X main.apiBase=..." 覆盖。
// 端到端测试构建（-tags e2e）里还可以用 MOEFRP_API_BASE 指定。
var apiBase = "https://frplus.loli-con.cn"

const (
	productName = "萌！FRP 高级客户端"
	uniqueID    = "com.moefrp.client"
)

// 退出时的停止参数（PLAN 6.3.8）：GracefulClose(500ms)，最多等 2 秒，超时直接退出。
const (
	quitGrace = 500 * time.Millisecond
	quitWait  = 2 * time.Second
)

const (
	wmEndSession = 0x0016
)

// 注册带类型的事件，绑定生成器据此给前端生成 TS 类型（PLAN 6.4）。
func init() {
	application.RegisterEvent[engine.Status](services.EventTunnelState)
	application.RegisterEvent[[]engine.ProxyStatus](services.EventTunnelProxy)
	application.RegisterEvent[[]engine.LogLine](services.EventTunnelLogs)
	application.RegisterEvent[cloudsync.AccountState](cloudsync.EventAuthState)
	application.RegisterEvent[*apierr.Error](cloudsync.EventAuthExpired)
	application.RegisterEvent[cloudsync.Status](cloudsync.EventSyncStatus)
	application.RegisterEvent[cloudsync.Changed](cloudsync.EventSyncChanged)
	application.RegisterEvent[cloudsync.Conflict](cloudsync.EventSyncConflict)
	application.RegisterEvent[cloudsync.RemoteUpdate](cloudsync.EventSyncRemote)
	application.RegisterEvent[cloudsync.PushError](cloudsync.EventSyncError)
	application.RegisterEvent[services.UpdateInfo](services.EventUpdateAvailable)
	application.RegisterEvent[string](services.EventAnnouncement)
}

var (
	diagMu   sync.Mutex
	diagFile *os.File
)

// diag 写一行诊断日志（只有端到端测试构建里设置了 MOEFRP_DIAG 才写）。
func diag(format string, args ...any) {
	diagMu.Lock()
	defer diagMu.Unlock()
	if diagFile == nil {
		return
	}
	fmt.Fprintf(diagFile, "%s pid=%d %s\n", time.Now().Format("15:04:05.000"), os.Getpid(), fmt.Sprintf(format, args...))
}

func processStart() time.Time {
	var c, e, k, u windows.Filetime
	if err := windows.GetProcessTimes(windows.CurrentProcess(), &c, &e, &k, &u); err != nil {
		return time.Now()
	}
	return time.Unix(0, c.Nanoseconds())
}

// setupLogs：致命错误写到 crash.log（PLAN 6.2）；标准库 log 和 stderr 写到 startup.log，
// 这样 Wails 在 app.Run 之前静默退出时（例如 WebView2 运行时损坏）至少能留下线索（PLAN 6.11）。
func setupLogs(paths store.Paths) {
	if f, err := os.OpenFile(paths.CrashLog(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
		fmt.Fprintf(f, "---- %s start %s (pid %d)\n", time.Now().Format(time.RFC3339), version, os.Getpid())
		if err := debug.SetCrashOutput(f, debug.CrashOptions{}); err != nil {
			fmt.Fprintf(f, "SetCrashOutput: %v\n", err)
		}
		_ = f.Close() // SetCrashOutput 自己持有一份句柄
	}
	if f, err := os.OpenFile(paths.StartupLog(), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600); err == nil {
		log.SetOutput(f)
		_ = windows.SetStdHandle(windows.STD_ERROR_HANDLE, windows.Handle(f.Fd()))
		os.Stderr = f
		log.Printf("start %s, pid %d", version, os.Getpid())
	}
}

// isolation 返回单实例锁的 ID 和凭据目标名。正式构建固定为 com.moefrp.client 和 MoeFrp/session；
// 端到端测试构建指定了数据目录时按目录区分，这样两个实例可以同时运行（模拟两台机器）。
func isolation(home string) (instance, credTarget string) {
	if home == "" {
		return uniqueID, credstore.DefaultTarget
	}
	abs, _ := filepath.Abs(home)
	sum := sha256.Sum256([]byte(abs))
	tag := hex.EncodeToString(sum[:4])
	return uniqueID + ".e2e-" + tag, credstore.DefaultTarget + "/e2e-" + tag
}

func main() {
	started := processStart()
	hooks := testhooks.Get()
	if hooks.Diag != "" {
		diagFile, _ = os.OpenFile(hooks.Diag, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	}
	diag("main() entered: %d ms since process creation, version=%s e2e=%v", time.Since(started).Milliseconds(), version, testhooks.Enabled)

	paths, err := store.DefaultPaths(hooks.Home)
	if err == nil {
		err = paths.Ensure()
	}
	if err != nil {
		fatalBox("无法创建数据目录：" + err.Error())
	}
	setupLogs(paths)
	requireWebView2(hooks.BrowserPath)

	var warnings []string
	settings, warn, err := store.OpenSettings(paths.SettingsFile())
	if err != nil {
		fatalBox("无法读取设置文件：" + err.Error())
	}
	if warn != "" {
		warnings = append(warnings, warn)
	}
	profiles, warn, err := store.OpenProfiles(paths.CacheFile())
	if err != nil {
		fatalBox("无法读取配置缓存：" + err.Error())
	}
	if warn != "" {
		warnings = append(warnings, warn)
	}

	// PLAN 6.3.6：进程启动时装一次 frp 全局日志出口
	sink := engine.NewLogSink(engine.DefaultLogLines)
	sink.InstallGlobal(settings.Get().LogLevel)
	for _, w := range warnings {
		sink.Appendf("warn", "%s", w)
		log.Print(w)
	}

	instanceID, credTarget := isolation(hooks.Home)
	base := apiBase
	if hooks.APIBase != "" {
		base = hooks.APIBase
	}
	api, err := apiclient.New(base, version, settings.Get().InstallID, settings.Get().NetworkProxy)
	if err != nil {
		fatalBox("服务端地址不正确：" + err.Error())
	}
	api.SetProxy(settings.Get().NetworkProxy, settings.Get().ProxyURL)
	hostname, _ := os.Hostname()

	eng := engine.New(sink, engine.Options{})
	syncMgr := cloudsync.New(cloudsync.Options{
		API: api, Creds: credstore.NewWinCred(credTarget), Profiles: profiles, Engine: eng, DeviceName: hostname,
		Logf: func(format string, args ...any) {
			log.Printf(format, args...)
			diag("cloud: "+format, args...)
		},
	})
	tunnelSvc, tunnelCtl := services.NewTunnelService(eng, sink, profiles, settings)
	profileSvc := services.NewProfileService(profiles, settings, eng, syncMgr)
	authSvc := services.NewAuthService(syncMgr, profiles, eng)
	settingsSvc := services.NewSettingsService(settings, sink)
	settingsSvc.OnNetworkProxy = api.SetProxy
	appSvc := services.NewAppService(version, paths, warnings, started)
	updater := services.NewUpdater(api, version)
	appSvc.Updater = updater
	imageSvc, imageCtl := services.NewImageService(paths.ImagesDir(), settings, func() *http.Client { return api.NewHTTPClient(30 * time.Second) })
	updater.OnConfig(func(cfg apiConfig) { imageCtl.SetSources(cfg.ImageSources) })

	// 开机自启（PLAN 6.8）：正式构建的值名是 MoeFrp；端到端测试构建按数据目录区分，不碰正式的那一项
	if exe, err := os.Executable(); err == nil {
		name := "MoeFrp"
		if instanceID != uniqueID {
			name = "MoeFrp-" + instanceID[len(uniqueID)+1:]
		}
		settingsSvc.AutoStart = &autostart.Entry{Name: name, Exe: exe}
		if err := settingsSvc.ReconcileAutoStart(); err != nil {
			log.Printf("autostart reconcile: %v", err)
		}
	}
	launchedAtBoot := autostart.Launched(os.Args[1:])
	updaterStop := make(chan struct{})
	shareSvc := services.NewShareService(syncMgr, profiles)

	var (
		app      *application.App
		winMu    sync.Mutex
		window   *application.WebviewWindow // 隐藏到托盘并释放 WebView2 后为 nil
		quitting atomic.Bool
		stopOnce sync.Once
	)
	currentWindow := func() *application.WebviewWindow {
		winMu.Lock()
		defer winMu.Unlock()
		return window
	}
	var newWindow func(visible bool) *application.WebviewWindow
	stopEngine := func(why string) {
		stopOnce.Do(func() {
			t0 := time.Now()
			err := eng.Shutdown(quitGrace, quitWait)
			diag("engine stopped (%s) in %d ms, err=%v", why, time.Since(t0).Milliseconds(), err)
			log.Printf("engine stopped (%s) in %s, err=%v", why, time.Since(t0), err)
		})
	}
	quit := func() {
		quitting.Store(true)
		app.Quit()
	}
	showWindow := func() {
		winMu.Lock()
		w := window
		if w == nil {
			t0 := time.Now()
			w = newWindow(true)
			window = w
			diag("window recreated in %d ms", time.Since(t0).Milliseconds())
		}
		winMu.Unlock()
		w.Show()
		w.Restore()
		w.Focus()
	}

	winOpts := application.WindowsOptions{
		DisableQuitOnLastWindowClosed: true,
		// 默认是 %APPDATA%\<exe 名>，发布的文件名带版本号，每次升级都会新建目录（坑 4）
		WebviewUserDataPath: paths.WebView2Dir(),
		WebviewBrowserPath:  hooks.BrowserPath,
		// 系统关机或注销时走和退出一样的停止流程（PLAN 6.3.8）
		WndProcInterceptor: func(hwnd uintptr, msg uint32, wParam, lParam uintptr) (uintptr, bool) {
			if msg == wmEndSession && wParam != 0 {
				diag("WM_ENDSESSION")
				stopEngine("end session")
			}
			return 0, false
		},
	}
	if p := hooks.CDPPort; p != "" {
		winOpts.AdditionalBrowserArgs = append(winOpts.AdditionalBrowserArgs, "--remote-debugging-port="+p,
			// 自动化时窗口经常被别的窗口挡住，WebView2 会暂停渲染和 CSS 过渡，截图和样式就停在旧状态
			"--disable-renderer-backgrounding", "--disable-backgrounding-occluded-windows")
		winOpts.DisabledFeatures = append(winOpts.DisabledFeatures, "CalculateNativeWinOcclusion")
	}

	app = application.New(application.Options{
		Name:        "MoeFrp",
		Description: productName,
		Services: []application.Service{
			application.NewService(appSvc),
			application.NewService(profileSvc),
			application.NewService(authSvc),
			application.NewService(tunnelSvc),
			application.NewService(settingsSvc),
			application.NewService(shareSvc),
			application.NewService(imageSvc),
		},
		// /moefrp-img/<id> 由图片面板提供（PLAN 6.9），其余是前端资源
		Assets:  application.AssetOptions{Handler: imageCtl.Handler(application.AssetFileServerFS(assets))},
		Windows: winOpts,
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: instanceID,
			OnSecondInstanceLaunch: func(data application.SecondInstanceData) {
				diag("second instance launched: args=%v", data.Args)
				showWindow()
			},
		},
		// beta.31 里缺 WebView2 的错误不会走到这里（坑），所以在 app.Run 前用 requireWebView2 预检
		ErrorHandler: func(err error) {
			diag("ErrorHandler: %v", err)
			log.Printf("wails error: %v", err)
		},
		OnShutdown: func() {
			diag("OnShutdown")
			close(updaterStop)
			tunnelCtl.Detach()
			syncMgr.Close()
			stopEngine("quit")
		},
	})

	newWindow = func(visible bool) *application.WebviewWindow {
		bg := application.NewRGB(250, 250, 252)
		if t := settings.Get().Theme; t == store.ThemeDark || (t == store.ThemeSystem && systemUsesDarkTheme()) {
			bg = application.NewRGB(24, 24, 28)
		}
		w := app.Window.NewWithOptions(application.WebviewWindowOptions{
			Name:             "main",
			Title:            productName,
			Width:            1120,
			Height:           740,
			MinWidth:         900,
			MinHeight:        600,
			BackgroundColour: bg,
			URL:              "/",
			Hidden:           !visible,
		})
		// 点关闭按钮：默认最小化到托盘，设置里可改为退出（PLAN 6.8）。
		// 最小化到托盘时默认关闭窗口、释放 WebView2，从托盘打开时重新创建；设置里关掉则只是隐藏。
		w.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
			if quitting.Load() {
				return
			}
			if settings.Get().CloseAction == store.CloseQuit {
				diag("WindowClosing -> quit")
				e.Cancel()
				go quit()
				return
			}
			if settings.Get().ReleaseOnHide {
				diag("WindowClosing -> close window, release WebView2")
				winMu.Lock()
				if window == w {
					window = nil
				}
				winMu.Unlock()
				return
			}
			diag("WindowClosing intercepted -> hide to tray")
			w.Hide()
			e.Cancel()
		})
		// 窗口获得焦点时拉一次云端状态（PLAN 6.6）
		w.OnWindowEvent(events.Common.WindowFocus, func(*application.WindowEvent) { syncMgr.Focus() })
		return w
	}
	// 开机自启时只显示托盘，不创建窗口（也就不启动 WebView2），从托盘打开时再创建
	if !launchedAtBoot {
		window = newWindow(true)
	} else {
		diag("launched at boot: tray only")
	}

	setupTray(app, tunnelSvc, tunnelCtl, showWindow, quit)

	tunnelCtl.SetSaveDialog(func(name string) (string, error) {
		return app.Dialog.SaveFile().
			SetFilename(name).
			AddFilter("日志文件", "*.log;*.txt").
			AttachToWindow(currentWindow()).
			PromptForSingleSelection()
	})
	imageCtl.SetSaveDialog(func(name string) (string, error) {
		return app.Dialog.SaveFile().
			SetFilename(name).
			AddFilter("图片", "*.png;*.jpg;*.jpeg;*.gif;*.webp;*.bmp").
			AttachToWindow(currentWindow()).
			PromptForSingleSelection()
	})
	appSvc.OpenBrowser = app.Browser.OpenURL
	appSvc.QuitApp = func() { go quit() }
	appSvc.OnReady = func(d time.Duration) { diag("frontend ready: %d ms since process creation", d.Milliseconds()) }

	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		diag("ApplicationStarted: %d ms since process creation", time.Since(started).Milliseconds())
		emit := func(name string, data any) { app.Event.Emit(name, data) }
		tunnelCtl.Attach(emit)
		syncMgr.SetEmitter(emit)
		// 用凭据管理器里的令牌自动登录（PLAN 6.4），网络请求在后台进行
		go syncMgr.Restore()
		// 检查更新、公告、图源（PLAN 6.12）：启动时一次，之后每 6 小时
		updater.SetEmitter(emit)
		go updater.Run(updaterStop)
		// 启动后自动连接上次的配置（PLAN 6.8，默认关闭）
		if settings.Get().AutoConnect {
			go func() {
				err := tunnelSvc.StartLast()
				diag("auto connect: err=%v", err)
				if err != nil {
					log.Printf("auto connect: %v", err)
				}
			}()
		}
	})

	if err := app.Run(); err != nil {
		diag("app.Run error: %v", err)
		log.Printf("app.Run: %v", err)
		stopEngine("run error")
		os.Exit(1)
	}
	stopEngine("run returned")
	diag("app.Run returned")
}

// setupTray：显示主界面、当前状态、启动/停止上次的配置、退出（PLAN 6.8）。
func setupTray(app *application.App, tunnel *services.TunnelService, ctl *services.TunnelControl, show func(), quit func()) {
	tray := app.SystemTray.New()
	tray.SetTooltip(productName)
	menu := app.NewMenu()
	menu.Add("显示主界面").OnClick(func(*application.Context) {
		diag("tray menu: show")
		show()
	})
	stateItem := menu.Add("状态：" + trayStateLabel(engine.StateIdle)).SetEnabled(false)
	toggleItem := menu.Add("启动上次的配置")
	toggleItem.OnClick(func(*application.Context) {
		diag("tray menu: toggle")
		s := tunnel.State()
		var err error
		switch {
		case s.Busy():
			return
		case s.Active():
			err = tunnel.Stop()
		default:
			err = tunnel.StartLast()
		}
		if err != nil {
			diag("tray toggle error: %v", err)
			show()
		}
	})
	menu.AddSeparator()
	menu.Add("退出").OnClick(func(*application.Context) {
		diag("tray menu: quit")
		quit()
	})
	tray.SetMenu(menu)
	tray.OnClick(func() {
		diag("tray left click")
		show()
	})

	ctl.OnStatus(func(s engine.Status) {
		label := "状态：" + trayStateLabel(s.State)
		if s.ProfileName != "" && s.State != engine.StateIdle {
			label += "（" + s.ProfileName + "）"
		}
		stateItem.SetLabel(label)
		switch {
		case s.Busy():
			toggleItem.SetLabel("正在停止…").SetEnabled(false)
		case s.Active():
			toggleItem.SetLabel("停止隧道").SetEnabled(true)
		default:
			toggleItem.SetLabel("启动上次的配置").SetEnabled(true)
		}
		tray.SetTooltip(productName + " - " + trayStateLabel(s.State))
		diag("tunnel state: %s profile=%q node=%q runID=%s logins=%d err=%v", s.State, s.ProfileName, s.NodeLabel, s.RunID, s.Logins, s.Error)
	})
}

func trayStateLabel(s engine.State) string {
	switch s {
	case engine.StateConnecting:
		return "连接中"
	case engine.StateRunning:
		return "运行中"
	case engine.StateReconnecting:
		return "重连中"
	case engine.StateFailed:
		return "启动失败"
	case engine.StateStopping:
		return "停止中"
	default:
		return "未运行"
	}
}

// apiConfig 是服务端下发的客户端配置。
type apiConfig = api.ClientConfigResponse

func fatalBox(msg string) {
	log.Print(msg)
	windows.MessageBox(0, windows.StringToUTF16Ptr(msg), windows.StringToUTF16Ptr(productName), windows.MB_ICONERROR|windows.MB_OK)
	os.Exit(1)
}
