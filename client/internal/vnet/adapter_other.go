//go:build !windows

package vnet

import "errors"

// IsAdmin：只支持 Windows。
func IsAdmin() bool { return false }

func configureAdapter(string, string) error { return errors.New("联机房间只支持 Windows") }

// DriverInstalled：只支持 Windows。
func DriverInstalled() bool { return false }

// InstallDriver：只支持 Windows。
func InstallDriver() error { return errors.New("联机房间只支持 Windows") }

// Shutdown：只支持 Windows。
func Shutdown() {}
