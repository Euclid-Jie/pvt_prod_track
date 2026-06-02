//go:build windows

package main

import "syscall"

func init() {
	user32 := syscall.NewLazyDLL("user32.dll")
	proc := user32.NewProc("SetProcessDpiAwarenessContext")
	if proc.Find() == nil {
		// DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 = -4
		proc.Call(^uintptr(3))
		return
	}
	// Fallback for older Windows
	shcore := syscall.NewLazyDLL("shcore.dll")
	if p := shcore.NewProc("SetProcessDpiAwareness"); p.Find() == nil {
		p.Call(2) // PROCESS_PER_MONITOR_DPI_AWARE
	}
}
