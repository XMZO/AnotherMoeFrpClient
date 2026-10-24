package services

import (
	"path/filepath"
	"testing"
)

func TestShortPath(t *testing.T) {
	home := t.TempDir()
	local := filepath.Join(home, "AppData", "Local")
	t.Setenv("USERPROFILE", home)
	t.Setenv("LOCALAPPDATA", local)
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	t.Setenv("TEMP", filepath.Join(local, "Temp"))
	sep := string(filepath.Separator)
	for in, want := range map[string]string{
		filepath.Join(home, "AppData", "Roaming", "MoeFrp"): "%APPDATA%" + sep + "MoeFrp",
		filepath.Join(local, "MoeFrp"):                      "%LOCALAPPDATA%" + sep + "MoeFrp",
		filepath.Join(local, "Temp", "x"):                   "%TEMP%" + sep + "x", // 更长的前缀优先
		filepath.Join(home, "Desktop"):                      "%USERPROFILE%" + sep + "Desktop",
		local + "x":                                         "%USERPROFILE%" + sep + filepath.Join("AppData", "Localx"), // 只是名字开头和 %LOCALAPPDATA% 相同
		filepath.Join(filepath.Dir(home), "other"):          filepath.Join(filepath.Dir(home), "other"),                 // 都不在里面：原样
	} {
		if got := shortPath(in); got != want {
			t.Errorf("shortPath(%q) = %q, want %q", in, got, want)
		}
	}
}
