package services

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// shortPath 把路径开头换成 Windows 的环境变量写法（例如 %APPDATA%\MoeFrp），"关于"页能完整显示、不用省略号。
// 按路径从长到短匹配（%TEMP% 在 %LOCALAPPDATA% 里面，%LOCALAPPDATA% 在 %USERPROFILE% 里面）；都不匹配时原样返回。
func shortPath(p string) string {
	clean := filepath.Clean(p)
	type env struct{ name, base string }
	var envs []env
	for _, name := range []string{"LOCALAPPDATA", "APPDATA", "TEMP", "USERPROFILE"} {
		if base := os.Getenv(name); base != "" {
			envs = append(envs, env{name, filepath.Clean(base)})
		}
	}
	slices.SortFunc(envs, func(a, b env) int { return len(b.base) - len(a.base) })
	for _, e := range envs {
		name, base := e.name, e.base
		if strings.EqualFold(clean, base) {
			return "%" + name + "%"
		}
		if len(clean) > len(base) && strings.EqualFold(clean[:len(base)], base) && os.IsPathSeparator(clean[len(base)]) {
			return "%" + name + "%" + clean[len(base):]
		}
	}
	return p
}
