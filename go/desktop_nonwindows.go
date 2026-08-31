//go:build !windows

package main

import "log"

func startDesktopApp() {
	log.Fatal("desktop mode is only supported on Windows")
}
