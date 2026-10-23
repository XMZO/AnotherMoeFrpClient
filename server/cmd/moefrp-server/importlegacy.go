package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"moefrp/server/internal/legacy"
)

// cmdImportLegacy 把旧版 users.db 导入到新库（PLAN 7.12）。目标库必须已执行 migrate 且没有用户。
func cmdImportLegacy(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs, cfgPath := newFlags("import-legacy", stderr)
	from := fs.String("from", "", "旧版 users.db 的路径（只读打开）")
	dryRun := fs.Bool("dry-run", false, "只输出报告，不写入")
	if rest, err := parseArgs(fs, args); err != nil {
		return err
	} else if len(rest) > 0 {
		return usagef("import-legacy 不接受参数：%s", strings.Join(rest, " "))
	}
	if *from == "" {
		return usagef("import-legacy 需要 --from")
	}
	if _, err := os.Stat(*from); err != nil {
		return fmt.Errorf("旧库 %s 不存在：%w", *from, err)
	}
	st, err := openExisting(ctx, *cfgPath, stderr)
	if err != nil {
		return err
	}
	defer st.Close()

	rep, err := legacy.Import(ctx, *from, st, legacy.ImportOptions{DryRun: *dryRun})
	if err != nil {
		return err
	}
	mode := "已导入"
	if rep.DryRun {
		mode = "试运行（未写入）"
	}
	fmt.Fprintf(stdout, "%s：用户 %d，配置 %d（节点 %d、代理 %d），邀请码 %d（已使用 %d）\n",
		mode, rep.Users, rep.Profiles, rep.Nodes, rep.Proxies, rep.Invites, rep.InvitesUsed)
	fmt.Fprintf(stdout, "分享 %d/%d，订阅 %d/%d（已导入/旧库总数）\n",
		rep.SharesImported, rep.Shares, rep.SubscriptionsImported, rep.Subscriptions)
	fmt.Fprintf(stdout, "问题 %d 条，其中跳过 %d 条\n", len(rep.Problems), rep.SkippedCount())
	for _, p := range rep.Problems {
		fmt.Fprintln(stdout, "  "+p.String())
	}
	return nil
}
