// Package store 是客户端的本地存储（PLAN 6.5）：设置、配置缓存和各种目录。
package store

import (
	"errors"
	"os"
	"path/filepath"
	"time"
)

// AppDirName 是 %APPDATA% / %LOCALAPPDATA% 下的目录名，和 exe 文件名无关（坑 4）。
const AppDirName = "MoeFrp"

// Paths 是所有本地文件的位置。
type Paths struct {
	Roaming string // %APPDATA%\MoeFrp：设置、配置缓存（随漫游配置文件走）
	Local   string // %LOCALAPPDATA%\MoeFrp：WebView2 数据、崩溃日志
}

// DefaultPaths 按 PLAN 6.5 返回默认位置。home 不为空时改用它下面的 Roaming、Local 两个子目录
// （只有端到端测试构建会传入，见 internal/testhooks）。
func DefaultPaths(home string) (Paths, error) {
	if home != "" {
		abs, err := filepath.Abs(home)
		if err != nil {
			return Paths{}, err
		}
		return Paths{Roaming: filepath.Join(abs, "Roaming"), Local: filepath.Join(abs, "Local")}, nil
	}
	roaming, err := os.UserConfigDir() // Windows 上是 %APPDATA%
	if err != nil {
		return Paths{}, err
	}
	local := os.Getenv("LOCALAPPDATA")
	if local == "" {
		if local, err = os.UserCacheDir(); err != nil {
			return Paths{}, err
		}
	}
	return Paths{Roaming: filepath.Join(roaming, AppDirName), Local: filepath.Join(local, AppDirName)}, nil
}

func (p Paths) SettingsFile() string { return filepath.Join(p.Roaming, "settings.json") }
func (p Paths) CacheFile() string    { return filepath.Join(p.Roaming, "cache.bin") }
func (p Paths) WebView2Dir() string  { return filepath.Join(p.Local, "WebView2") }
func (p Paths) CrashLog() string     { return filepath.Join(p.Local, "crash.log") }
func (p Paths) StartupLog() string   { return filepath.Join(p.Local, "startup.log") }
func (p Paths) ImagesDir() string    { return filepath.Join(p.Local, "images") }

// Ensure 创建两个数据目录。
func (p Paths) Ensure() error {
	return errors.Join(os.MkdirAll(p.Roaming, 0o700), os.MkdirAll(p.Local, 0o700))
}

// writeFileAtomic 先写临时文件再改名，避免写到一半断电留下半个文件。
func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	_, werr := tmp.Write(data)
	serr := tmp.Sync()
	cerr := tmp.Close()
	if err := errors.Join(werr, serr, cerr); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
}

// quarantine 把读不出来的文件改名留作备份，返回新文件名。
func quarantine(path string) string {
	bad := path + ".bad-" + time.Now().Format("20060102-150405")
	if err := os.Rename(path, bad); err != nil {
		return ""
	}
	return bad
}
