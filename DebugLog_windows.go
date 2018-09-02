// +build windows

package main

import (
	"runtime"
	"syscall"
	"unsafe"
)


func DebugLog(msg string) {
	if runtime.GOOS != "windows" {
		return
	}

	pm := syscall.StringToUTF16Ptr(msg)
	d, err := syscall.LoadDLL("kernel32.dll")
	if err != nil {
		return
	}

	p, err2 := d.FindProc("OutputDebugStringW")
	if err2 != nil {
		return
	}
	p.Call(uintptr(unsafe.Pointer(pm)))
}

