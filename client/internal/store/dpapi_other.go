//go:build !windows

package store

import "errors"

// 客户端只发布 Windows 版；其他平台只为了让包能编译和测试。
func protect(plain []byte) ([]byte, error)    { return nil, errors.ErrUnsupported }
func unprotect(cipher []byte) ([]byte, error) { return nil, errors.ErrUnsupported }
