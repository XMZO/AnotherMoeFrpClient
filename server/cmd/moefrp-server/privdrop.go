package main

// 容器里以 root 启动时，先把数据目录的属主改成 MOEFRP_PUID / MOEFRP_PGID 指定的用户，
// 再永久切换到这个用户继续运行（和 gosu、su-exec 是同一个思路）。这样：
//   - 绑定挂载的 ./data 不管是 Docker 以 root 建的、还是从别的机器拷来的，都能直接用；
//   - 进程本身不以 root 运行；
//   - 把 PUID/PGID 设成宿主机上自己的 id，./data 里的文件就归自己，不用 sudo 也能打包。
// 没设置 MOEFRP_PUID 时什么都不做（systemd 部署由 User= 决定用户）。具体实现只在 Linux 上有。

import (
	"fmt"
	"strconv"
	"strings"
)

type runAs struct{ uid, gid int }

// parseRunAs 读取 MOEFRP_PUID / MOEFRP_PGID。PGID 不设时等于 PUID；PUID 不设时返回 ok = false。
func parseRunAs(lookup func(string) (string, bool)) (r runAs, ok bool, err error) {
	parse := func(name string) (int, bool, error) {
		v, set := lookup(name)
		if !set || strings.TrimSpace(v) == "" {
			return 0, false, nil
		}
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil || n < 0 {
			return 0, false, fmt.Errorf("%s 应为非负整数：%q", name, v)
		}
		return n, true, nil
	}
	uid, ok, err := parse("MOEFRP_PUID")
	if err != nil || !ok {
		return runAs{}, false, err
	}
	gid, gset, err := parse("MOEFRP_PGID")
	if err != nil {
		return runAs{}, false, err
	}
	if !gset {
		gid = uid
	}
	return runAs{uid: uid, gid: gid}, true, nil
}

// initialUserNamespace 判断 /proc/self/uid_map 是否是宿主机的原始映射（"0 0 4294967295"）。
// rootless Docker、Podman 里容器的 root 映射到宿主机上的普通用户，这时不需要也不应该再降权：
// 降到 65532 会让宿主机上 ./data 里的文件属于一个陌生的子 uid。
func initialUserNamespace(uidMap string) bool {
	f := strings.Fields(uidMap)
	return len(f) >= 3 && f[0] == "0" && f[1] == "0" && f[2] == "4294967295"
}
