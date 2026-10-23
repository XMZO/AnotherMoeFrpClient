package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"moefrp/server/internal/admin"
	"moefrp/server/internal/auth"
	"moefrp/server/internal/store"
)

// openExisting 打开已存在且已迁移的数据库，管理命令和备份都不应该凭空建一个新库。
func openExisting(ctx context.Context, cfgPath string, stderr io.Writer) (*store.Store, *admin.Admin, error) {
	cfg, path, err := loadConfig(cfgPath)
	if err != nil {
		return nil, nil, err
	}
	if path == "" {
		path = "（未使用配置文件）"
	}
	fmt.Fprintf(stderr, "配置：%s  数据库：%s\n", path, cfg.DBPath)
	if _, err := os.Stat(cfg.DBPath); err != nil {
		return nil, nil, fmt.Errorf("数据库 %s 不存在，请检查 -c 或先执行 migrate：%w", cfg.DBPath, err)
	}
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		return nil, nil, err
	}
	if err := st.CheckSchema(ctx); err != nil {
		st.Close()
		return nil, nil, err
	}
	// 管理命令不做密码哈希，用最轻的参数创建认证服务，避免启动时白算一次 64MB 的假哈希。
	opt := authOptions(cfg, slog.New(slog.NewTextHandler(stderr, nil)))
	opt.Params = auth.Params{Memory: 8, Time: 1, Threads: 1, KeyLen: 16, SaltLen: 8}
	a, err := auth.New(st, opt)
	if err != nil {
		st.Close()
		return nil, nil, err
	}
	return st, admin.New(st, a, nil), nil
}

const timeLayout = "2006-01-02 15:04"

func fmtTime(unix int64) string {
	if unix == 0 {
		return "-"
	}
	return time.Unix(unix, 0).Format(timeLayout)
}

func fmtTimePtr(p *int64) string {
	if p == nil {
		return "-"
	}
	return fmtTime(*p)
}

func cmdAdmin(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) < 2 {
		return usagef("admin 需要子命令，例如 admin invite create")
	}
	group, cmd, rest := args[0], args[1], args[2:]
	switch group + " " + cmd {
	case "invite create":
		return adminInviteCreate(ctx, rest, stdout, stderr)
	case "invite list":
		return adminInviteList(ctx, rest, stdout, stderr)
	case "invite revoke":
		return adminInviteRevoke(ctx, rest, stdout, stderr)
	case "user list":
		return adminUserList(ctx, rest, stdout, stderr)
	case "user set-role":
		return adminUserSetRole(ctx, rest, stdout, stderr)
	case "user disable", "user enable", "user kick", "user reset-code":
		return adminUserSimple(ctx, cmd, rest, stdout, stderr)
	}
	return usagef("未知的管理命令：admin %s %s", group, cmd)
}

func adminInviteCreate(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs, cfgPath := newFlags("admin invite create", stderr)
	n := fs.Int("n", 1, "生成的数量")
	note := fs.String("note", "", "备注，例如给谁的")
	if rest, err := parseArgs(fs, args); err != nil {
		return err
	} else if len(rest) > 0 {
		return usagef("invite create 不接受参数：%s", strings.Join(rest, " "))
	}
	st, a, err := openExisting(ctx, *cfgPath, stderr)
	if err != nil {
		return err
	}
	defer st.Close()
	codes, err := a.CreateInvites(ctx, *n, *note)
	if err != nil {
		return err
	}
	for _, c := range codes {
		fmt.Fprintln(stdout, c)
	}
	return nil
}

func adminInviteList(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs, cfgPath := newFlags("admin invite list", stderr)
	unused := fs.Bool("unused", false, "只列出未使用的")
	if rest, err := parseArgs(fs, args); err != nil {
		return err
	} else if len(rest) > 0 {
		return usagef("invite list 不接受参数：%s", strings.Join(rest, " "))
	}
	st, a, err := openExisting(ctx, *cfgPath, stderr)
	if err != nil {
		return err
	}
	defer st.Close()
	invites, err := a.ListInvites(ctx, *unused)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "CODE\tSTATUS\tCREATED\tUSED_BY\tUSED_AT\tNOTE") // 英文表头：tabwriter 按字符数对齐，中文会错位
	for _, inv := range invites {
		status := "unused"
		switch {
		case inv.RevokedAt != nil:
			status = "revoked"
		case inv.UsedAt != nil:
			status = "used"
		}
		by := inv.UsedByNickname
		if by == "" {
			by = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", inv.Code, status, fmtTime(inv.CreatedAt), by, fmtTimePtr(inv.UsedAt), inv.Note)
	}
	return tw.Flush()
}

func adminInviteRevoke(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs, cfgPath := newFlags("admin invite revoke", stderr)
	rest, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return usagef("用法：admin invite revoke FRPT-XXXX-XXXX")
	}
	st, a, err := openExisting(ctx, *cfgPath, stderr)
	if err != nil {
		return err
	}
	defer st.Close()
	if err := a.RevokeInvite(ctx, rest[0]); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "已撤销 %s\n", strings.ToUpper(strings.TrimSpace(rest[0])))
	return nil
}

func adminUserList(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs, cfgPath := newFlags("admin user list", stderr)
	if rest, err := parseArgs(fs, args); err != nil {
		return err
	} else if len(rest) > 0 {
		return usagef("user list 不接受参数：%s", strings.Join(rest, " "))
	}
	st, a, err := openExisting(ctx, *cfgPath, stderr)
	if err != nil {
		return err
	}
	defer st.Close()
	users, err := a.ListUsers(ctx)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NICKNAME\tROLE\tSTATUS\tCREATED\tLAST_SEEN\tSESSIONS")
	for _, u := range users {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%d\n", u.Nickname, u.Role, u.Status, fmtTime(u.CreatedAt), fmtTime(u.LastSeen), u.ActiveSessions)
	}
	return tw.Flush()
}

func adminUserSetRole(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs, cfgPath := newFlags("admin user set-role", stderr)
	rest, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 2 {
		return usagef("用法：admin user set-role <昵称> user|vip|admin")
	}
	st, a, err := openExisting(ctx, *cfgPath, stderr)
	if err != nil {
		return err
	}
	defer st.Close()
	if err := a.SetRole(ctx, rest[0], rest[1]); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "已把 %s 的角色设为 %s\n", rest[0], rest[1])
	return nil
}

func adminUserSimple(ctx context.Context, cmd string, args []string, stdout, stderr io.Writer) error {
	fs, cfgPath := newFlags("admin user "+cmd, stderr)
	rest, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return usagef("用法：admin user %s <昵称>", cmd)
	}
	nick := rest[0]
	st, a, err := openExisting(ctx, *cfgPath, stderr)
	if err != nil {
		return err
	}
	defer st.Close()
	switch cmd {
	case "disable":
		n, err := a.Disable(ctx, nick)
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "已停用 %s，吊销了 %d 个会话\n", nick, n)
	case "enable":
		if err := a.Enable(ctx, nick); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "已启用 %s\n", nick)
	case "kick":
		n, err := a.Kick(ctx, nick)
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "已吊销 %s 的 %d 个会话\n", nick, n)
	case "reset-code":
		code, exp, err := a.ResetCode(ctx, nick)
		if err != nil {
			return err
		}
		// 重置码只打印到 stdout 这一次；库里只存哈希
		fmt.Fprintf(stdout, "%s\n", code)
		fmt.Fprintf(stderr, "重置码有效期至 %s，请通过可信渠道交给 %s\n", exp.Format(timeLayout), nick)
	default:
		return errors.New("unreachable")
	}
	return nil
}
