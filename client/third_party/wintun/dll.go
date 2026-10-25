//go:build windows

/* SPDX-License-Identifier: MIT
 *
 * Copyright (C) 2017-2021 WireGuard LLC. All Rights Reserved.
 *
 * MoeFrp 的修改：wintun.dll 编进程序（embed.go），用 memmod 直接从内存加载，
 * 不在磁盘上释放文件（和 WireGuard 官方 Windows 客户端的做法一样）。
 */

package wintun

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"

	"golang.zx2c4.com/wireguard/windows/driver/memmod"
)

func newLazyDLL(name string, onLoad func(d *lazyDLL)) *lazyDLL {
	return &lazyDLL{Name: name, onLoad: onLoad}
}

func (d *lazyDLL) NewProc(name string) *lazyProc {
	return &lazyProc{dll: d, Name: name}
}

type lazyProc struct {
	Name string
	mu   sync.Mutex
	dll  *lazyDLL
	addr uintptr
}

func (p *lazyProc) Find() error {
	if atomic.LoadPointer((*unsafe.Pointer)(unsafe.Pointer(&p.addr))) != nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.addr != 0 {
		return nil
	}

	err := p.dll.Load()
	if err != nil {
		return fmt.Errorf("Error loading %v DLL: %w", p.dll.Name, err)
	}
	addr, err := p.nameToAddr()
	if err != nil {
		return fmt.Errorf("Error getting %v address: %w", p.Name, err)
	}

	atomic.StorePointer((*unsafe.Pointer)(unsafe.Pointer(&p.addr)), unsafe.Pointer(addr))
	return nil
}

func (p *lazyProc) Addr() uintptr {
	err := p.Find()
	if err != nil {
		panic(err)
	}
	return p.addr
}

type lazyDLL struct {
	Name   string
	mu     sync.Mutex
	module *memmod.Module
	onLoad func(d *lazyDLL)
}

func (d *lazyDLL) Load() error {
	if atomic.LoadPointer((*unsafe.Pointer)(unsafe.Pointer(&d.module))) != nil {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.module != nil {
		return nil
	}
	if len(dllBytes) == 0 {
		return errors.New("这个版本没有内置 wintun.dll（只支持 Windows x64）")
	}
	module, err := memmod.LoadLibrary(dllBytes)
	if err != nil {
		return fmt.Errorf("Unable to load library: %w", err)
	}

	atomic.StorePointer((*unsafe.Pointer)(unsafe.Pointer(&d.module)), unsafe.Pointer(module))
	if d.onLoad != nil {
		d.onLoad(d)
	}
	return nil
}

func (p *lazyProc) nameToAddr() (uintptr, error) {
	return p.dll.module.ProcAddressByName(p.Name)
}

// Version 返回内置 wintun.dll 的版本（从内存加载拿不到版本资源，写死）。
func Version() string { return "0.14.1 (embedded)" }

// DisableLogger 清掉 wintun 的日志回调（MoeFrp 加的）。程序退出前调用：ExitProcess 过程中 wintun 再回调进 Go
// 会让 Go 运行时崩溃（主线程卡在退出里，窗口"未响应"）。没加载过 wintun.dll 时什么都不做。
func DisableLogger() {
	if atomic.LoadPointer((*unsafe.Pointer)(unsafe.Pointer(&modwintun.module))) == nil {
		return
	}
	if p := modwintun.NewProc("WintunSetLogger"); p.Find() == nil {
		syscall.SyscallN(p.Addr(), 0)
	}
}
