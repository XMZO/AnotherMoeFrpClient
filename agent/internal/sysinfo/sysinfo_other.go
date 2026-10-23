//go:build !linux

package sysinfo

import "os"

// ReadStatic 在非 Linux 系统上只返回主机名（agent 只支持 Linux，这里用于开发和测试）。
func ReadStatic() Static {
	host, _ := os.Hostname()
	return Static{Hostname: host}
}

// Sample 在非 Linux 系统上返回空值。
func (s *Sampler) Sample() Usage { return Usage{} }
