// Package webadmin 是管理后台的静态页面（原生 JS，无构建步骤），编译进服务端二进制。
package webadmin

import (
	"embed"
	"io/fs"
)

//go:embed static
var static embed.FS

// FS 返回页面文件，根目录下是 index.html、app.js、app.css。
func FS() fs.FS {
	sub, err := fs.Sub(static, "static")
	if err != nil {
		panic(err)
	}
	return sub
}
