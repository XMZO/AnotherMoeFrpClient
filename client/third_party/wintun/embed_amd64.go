//go:build windows && amd64

package wintun

import _ "embed"

// dllBytes 是 https://www.wintun.net 的 wintun-0.14.1.zip 里 bin/amd64/wintun.dll（WireGuard LLC 签名）。
//
//go:embed wintun.dll
var dllBytes []byte
