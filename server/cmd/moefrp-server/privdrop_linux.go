package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// dropPrivileges 见 privdrop.go。必须在打开数据库、启动任何后台 goroutine 之前调用。
func dropPrivileges(dataDir string, stderr io.Writer) error {
	ra, ok, err := parseRunAs(os.LookupEnv)
	if err != nil || !ok {
		return err
	}
	if os.Getuid() != 0 || ra.uid == 0 {
		return nil // 已经是普通用户（例如 compose 里写了 user:），或者明确要求以 root 运行
	}
	if b, err := os.ReadFile("/proc/self/uid_map"); err == nil && !initialUserNamespace(string(b)) {
		return nil
	}
	// 只处理明确的数据目录：相对路径或根目录会让下面的 chown 波及整个文件系统
	if !filepath.IsAbs(dataDir) || filepath.Clean(dataDir) == "/" {
		return fmt.Errorf("设置了 MOEFRP_PUID 时 db_path 必须是独立数据目录下的绝对路径，当前数据目录是 %q", dataDir)
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	n, err := chownTree(dataDir, ra)
	if err != nil {
		return fmt.Errorf("修改 %s 的属主：%w", dataDir, err)
	}
	if n > 0 {
		fmt.Fprintf(stderr, "已把 %s 下 %d 个文件和目录的属主改为 %d:%d\n", dataDir, n, ra.uid, ra.gid)
	}
	// 顺序：先清附加组、再改组、最后改用户（改了用户之后就没有权限改组了）。
	// Go 1.16 起这几个调用在 Linux 上对所有线程生效。
	if err := syscall.Setgroups([]int{}); err != nil {
		return fmt.Errorf("setgroups: %w", err)
	}
	if err := syscall.Setgid(ra.gid); err != nil {
		return fmt.Errorf("setgid %d: %w", ra.gid, err)
	}
	if err := syscall.Setuid(ra.uid); err != nil {
		return fmt.Errorf("setuid %d: %w", ra.uid, err)
	}
	return nil
}

// chownTree 把 dir 及其下所有文件的属主改成 ra，返回实际改动的个数。不跟随符号链接。
func chownTree(dir string, ra runAs) (int, error) {
	n := 0
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if st, ok := info.Sys().(*syscall.Stat_t); ok && int(st.Uid) == ra.uid && int(st.Gid) == ra.gid {
			return nil
		}
		n++
		return os.Lchown(p, ra.uid, ra.gid)
	})
	return n, err
}
