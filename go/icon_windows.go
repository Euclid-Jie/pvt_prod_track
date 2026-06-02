//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

func setWindowIcon(hwnd unsafe.Pointer) {
	user32 := syscall.NewLazyDLL("user32.dll")
	loadImage := user32.NewProc("LoadImageW")
	sendMessage := user32.NewProc("SendMessageW")

	iconPath, _ := syscall.UTF16PtrFromString("icon.ico")
	for _, size := range []uintptr{16, 32} {
		hicon, _, _ := loadImage.Call(0, uintptr(unsafe.Pointer(iconPath)), 1, size, size, 0x10)
		if hicon != 0 {
			kind := uintptr(0)
			if size == 32 {
				kind = 1
			}
			sendMessage.Call(uintptr(hwnd), 0x0080, kind, hicon)
		}
	}
}
