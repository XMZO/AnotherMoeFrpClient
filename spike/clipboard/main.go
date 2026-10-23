// 验证 golang.design/x/clipboard 在 Windows 上能否用 CGO_ENABLED=0 编译并初始化。
// 默认只调用 Init，不读写剪贴板；加 -write-image 时会往剪贴板写一张 1x1 PNG 并读回校验。
package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"runtime"

	"golang.design/x/clipboard"
)

func main() {
	writeImage := flag.Bool("write-image", false, "write a 1x1 PNG to the clipboard and read it back")
	flag.Parse()
	fmt.Printf("go=%s os=%s arch=%s\n", runtime.Version(), runtime.GOOS, runtime.GOARCH)
	if err := clipboard.Init(); err != nil {
		fmt.Println("Init error:", err)
		os.Exit(1)
	}
	fmt.Println("Init OK")
	if *writeImage {
		img := image.NewRGBA(image.Rect(0, 0, 1, 1))
		img.Set(0, 0, color.RGBA{255, 0, 0, 255})
		var buf bytes.Buffer
		_ = png.Encode(&buf, img)
		ctx := context.Background()
		if _, err := clipboard.Write(ctx, clipboard.FmtImage, buf.Bytes()); err != nil {
			fmt.Println("Write error:", err)
			os.Exit(1)
		}
		got, err := clipboard.Read(ctx, clipboard.FmtImage)
		fmt.Printf("image round trip: wrote %d bytes, read %d bytes, err=%v\n", buf.Len(), len(got), err)
	}
}
