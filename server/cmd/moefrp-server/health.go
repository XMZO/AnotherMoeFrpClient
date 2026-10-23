package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// cmdHealth 请求本机服务的 /healthz，正常时退出码 0。镜像里没有 curl，容器健康检查用它。
func cmdHealth(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs, cfgPath := newFlags("health", stderr)
	if rest, err := parseArgs(fs, args); err != nil {
		return err
	} else if len(rest) > 0 {
		return usagef("health 不接受参数：%s", strings.Join(rest, " "))
	}
	cfg, _, err := loadConfig(*cfgPath)
	if err != nil {
		return err
	}
	_, port, err := net.SplitHostPort(cfg.Listen)
	if err != nil {
		return fmt.Errorf("listen 地址 %q 不正确：%w", cfg.Listen, err)
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:"+port+"/healthz", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthz 返回 %d：%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	fmt.Fprint(stdout, string(body))
	return nil
}
