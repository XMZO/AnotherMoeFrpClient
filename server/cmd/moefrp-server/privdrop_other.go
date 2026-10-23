//go:build !linux

package main

import "io"

// dropPrivileges 只在 Linux（容器）上有意义，见 privdrop.go。
func dropPrivileges(dataDir string, stderr io.Writer) error { return nil }
