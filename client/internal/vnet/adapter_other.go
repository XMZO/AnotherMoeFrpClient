//go:build !windows

package vnet

import "errors"

// IsAdmin：只支持 Windows。
func IsAdmin() bool { return false }

func configureAdapter(string, string) error { return errors.New("联机房间只支持 Windows") }
