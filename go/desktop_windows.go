//go:build windows

package main

import (
	"log"
	"net"
	"net/http"

	webview "github.com/jchv/go-webview2"
)

func startDesktopApp() {
	mux := newAppMux("assets/templates/index.html")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatal(err)
	}
	serverURL := "http://" + listener.Addr().String()
	go func() {
		if err := http.Serve(listener, mux); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()

	w := webview.New(false)
	if w == nil {
		log.Fatal("WebView2 初始化失败，请确认系统已安装 WebView2 Runtime")
	}
	defer w.Destroy()
	w.SetTitle("私募产品周报")
	w.SetSize(1400, 860, webview.HintNone)
	setWindowIcon(w.Window())
	w.Navigate(serverURL)
	w.Run()
}
