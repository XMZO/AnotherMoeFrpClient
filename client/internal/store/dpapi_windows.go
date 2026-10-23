//go:build windows

package store

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// dpapiEntropy 让别的程序不能直接用 CryptUnprotectData 解出我们的缓存（同一用户的程序仍然可以，
// 这不是安全边界，PLAN 8.1：客户端里的一切都能被用户自己看到）。
var dpapiEntropy = []byte("MoeFrp/cache/v1")

// protect 用 DPAPI（当前用户）加密。
func protect(plain []byte) ([]byte, error) {
	return dpapi(plain, true)
}

// unprotect 用 DPAPI 解密；换了用户或换了机器时会失败。
func unprotect(cipher []byte) ([]byte, error) {
	return dpapi(cipher, false)
}

func dpapi(in []byte, encrypt bool) ([]byte, error) {
	if len(in) == 0 {
		in = []byte{0} // DPAPI 不接受空输入的 nil 指针；调用方保证明文是非空 JSON
	}
	inBlob := windows.DataBlob{Size: uint32(len(in)), Data: &in[0]}
	entropy := windows.DataBlob{Size: uint32(len(dpapiEntropy)), Data: &dpapiEntropy[0]}
	var out windows.DataBlob
	var err error
	if encrypt {
		err = windows.CryptProtectData(&inBlob, nil, &entropy, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out)
	} else {
		err = windows.CryptUnprotectData(&inBlob, nil, &entropy, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out)
	}
	if err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return append([]byte(nil), unsafe.Slice(out.Data, out.Size)...), nil
}
