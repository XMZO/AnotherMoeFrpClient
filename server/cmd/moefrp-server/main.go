// moefrp-server 是 MoeFrp 的服务端：账号、云端配置同步、版本信息（PLAN 7）。
//
//	moefrp-server serve   -c config.toml [--auto-migrate]
//	moefrp-server migrate -c config.toml
//	moefrp-server backup  -c config.toml --to /path/backup.db
//	moefrp-server import-legacy -c config.toml --from users.db [--dry-run]
//	moefrp-server version
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"moefrp/server/internal/auth"
	"moefrp/server/internal/config"
	"moefrp/server/internal/httpapi"
	"moefrp/server/internal/profile"
	"moefrp/server/internal/store"
)

// version 在构建时用 -ldflags "-X main.version=<版本>" 设置。
var version = "dev"

// defaultConfigPaths 是没指定 -c 和 MOEFRP_CONFIG 时依次尝试的配置文件：
// 服务器上的 /etc/moefrp/config.toml，以及 Docker 镜像数据目录里的 /data/config.toml。
// 都不存在时只用默认值和环境变量。
var defaultConfigPaths = []string{"/etc/moefrp/config.toml", "/data/config.toml"}

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}

const usage = `用法：
  moefrp-server serve    [-c config.toml] [--auto-migrate]
  moefrp-server migrate  [-c config.toml]
  moefrp-server backup   [-c config.toml] --to /path/backup.db
  moefrp-server import-legacy [-c config.toml] --from users.db [--dry-run]
  moefrp-server health   [-c config.toml]     # 检查本机服务是否正常（容器健康检查用）
  moefrp-server version

-c 未指定时依次尝试环境变量 MOEFRP_CONFIG、/etc/moefrp/config.toml 和 /data/config.toml，
都没有时只使用默认值和 MOEFRP_* 环境变量。

邀请码、用户管理、审计日志和备份下载在网页管理后台：http(s)://<服务地址>/admin/
第一次打开时用启动日志里的初始化码创建管理员。
`

// run 是可测试的入口，返回进程退出码。
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	var err error
	switch args[0] {
	case "serve":
		err = cmdServe(ctx, args[1:], stdout, stderr)
	case "migrate":
		err = cmdMigrate(ctx, args[1:], stdout, stderr)
	case "backup":
		err = cmdBackup(ctx, args[1:], stdout, stderr)
	case "admin":
		err = errors.New("命令行管理已移除，请打开网页管理后台 /admin/（首次使用的初始化码在 serve 的启动日志里）")
	case "import-legacy":
		err = cmdImportLegacy(ctx, args[1:], stdout, stderr)
	case "health":
		err = cmdHealth(ctx, args[1:], stdout, stderr)
	case "version", "--version", "-v":
		fmt.Fprintln(stdout, version)
		return 0
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "未知命令 %q\n\n%s", args[0], usage)
		return 2
	}
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		var ue usageError
		if errors.As(err, &ue) {
			fmt.Fprintf(stderr, "%s\n\n%s", ue.msg, usage)
			return 2
		}
		fmt.Fprintln(stderr, "错误：", err)
		return 1
	}
	return 0
}

type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

func usagef(format string, args ...any) error { return usageError{fmt.Sprintf(format, args...)} }

// newFlags 创建子命令的 FlagSet，统一带上 -c。
func newFlags(name string, stderr io.Writer) (*flag.FlagSet, *string) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfg := fs.String("c", "", "配置文件路径")
	return fs, cfg
}

// parseArgs 允许参数和选项交错出现，例如 `user set-role alice vip -c x.toml`。
func parseArgs(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			return pos, nil
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
}

// loadConfig 按 -c、MOEFRP_CONFIG、默认路径的顺序找配置文件。
func loadConfig(path string) (config.Config, string, error) {
	if path == "" {
		path = os.Getenv("MOEFRP_CONFIG")
	}
	for _, p := range defaultConfigPaths {
		if path != "" {
			break
		}
		if _, err := os.Stat(p); err == nil {
			path = p
		}
	}
	cfg, err := config.Load(path)
	return cfg, path, err
}

func newLogger(w io.Writer, level string) *slog.Logger {
	var l slog.Level
	_ = l.UnmarshalText([]byte(strings.ToUpper(level)))
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: l}))
}

func authOptions(cfg config.Config, log *slog.Logger) auth.Options {
	return auth.Options{
		SingleSession: cfg.SingleSession,
		SessionTTL:    cfg.SessionTTL.D(),
		Logger:        log,
	}
}

// ---- serve ----

func cmdServe(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs, cfgPath := newFlags("serve", stderr)
	autoMigrate := fs.Bool("auto-migrate", false, "启动前自动执行未执行的迁移（不先备份，生产环境请用 migrate）")
	if rest, err := parseArgs(fs, args); err != nil {
		return err
	} else if len(rest) > 0 {
		return usagef("serve 不接受参数：%s", strings.Join(rest, " "))
	}
	cfg, path, err := loadConfig(*cfgPath)
	if err != nil {
		return err
	}
	log := newLogger(stdout, cfg.LogLevel)
	slog.SetDefault(log)

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer st.Close()
	if *autoMigrate {
		// 已有数据的库在升级前自动备份一份（容器部署时没人手动执行 backup）
		if pending, err := st.Pending(ctx); err == nil && len(pending) > 0 {
			if applied, err := st.AppliedVersions(ctx); err == nil && len(applied) > 0 {
				to := cfg.DBPath + ".pre-migrate-" + time.Now().Format("20060102-150405") + ".db"
				if err := st.Backup(ctx, to); err != nil {
					return fmt.Errorf("升级数据库前的自动备份失败：%w", err)
				}
				log.Info("backup before migration", "to", to)
			}
		}
		done, err := st.Migrate(ctx)
		if err != nil {
			return err
		}
		for _, m := range done {
			log.Info("migration applied", "name", m.Name)
		}
	}
	if err := st.CheckSchema(ctx); err != nil {
		return err
	}

	authSvc, err := auth.New(st, authOptions(cfg, log))
	if err != nil {
		return err
	}
	srv, err := httpapi.New(httpapi.Deps{
		Config:   cfg,
		Store:    st,
		Auth:     authSvc,
		Profiles: profile.New(st, nil),
		Logger:   log,
		Version:  version,
	})
	if err != nil {
		return err
	}

	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return err
	}
	httpSrv := &http.Server{
		Handler:           srv,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    32 << 10,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	go srv.RunMaintenance(ctx, time.Minute)

	log.Info("moefrp-server started",
		"version", version, "listen", ln.Addr().String(), "config", path, "db", cfg.DBPath,
		"single_session", cfg.SingleSession, "min_client_version", cfg.Client.MinVersion)

	errc := make(chan error, 1)
	go func() { errc <- httpSrv.Serve(ln) }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	if err := <-errc; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// ---- migrate ----

func cmdMigrate(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs, cfgPath := newFlags("migrate", stderr)
	if rest, err := parseArgs(fs, args); err != nil {
		return err
	} else if len(rest) > 0 {
		return usagef("migrate 不接受参数：%s", strings.Join(rest, " "))
	}
	cfg, _, err := loadConfig(*cfgPath)
	if err != nil {
		return err
	}
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer st.Close()
	done, err := st.Migrate(ctx)
	for _, m := range done {
		fmt.Fprintf(stdout, "已执行 %s\n", m.Name)
	}
	if err != nil {
		return err
	}
	if len(done) == 0 {
		fmt.Fprintf(stdout, "数据库 %s 已是最新\n", cfg.DBPath)
	}
	return nil
}

// ---- backup ----

func cmdBackup(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs, cfgPath := newFlags("backup", stderr)
	to := fs.String("to", "", "备份文件路径（不能已存在）")
	if rest, err := parseArgs(fs, args); err != nil {
		return err
	} else if len(rest) > 0 {
		return usagef("backup 不接受参数：%s", strings.Join(rest, " "))
	}
	if *to == "" {
		return usagef("backup 需要 --to")
	}
	cfg, _, err := loadConfig(*cfgPath)
	if err != nil {
		return err
	}
	if _, err := os.Stat(cfg.DBPath); err != nil {
		return fmt.Errorf("数据库 %s 不存在：%w", cfg.DBPath, err)
	}
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.Backup(ctx, *to); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "已备份到 %s\n", *to)
	return nil
}
