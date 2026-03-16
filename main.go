package main

import (
	"embed"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
)

//go:embed ui/index.html ui/i18n
var uiFiles embed.FS

var (
	appDir    string
	libDir    string
	configDir string
)

//go:embed icon.ico
var appIcon []byte

func init() {
	exe, err := os.Executable()
	if err != nil {
		log.Fatal(err)
	}
	appDir = filepath.Dir(exe)
	libDir = filepath.Join(appDir, "lib")
	configDir = filepath.Join(appDir, "configs")
	_ = os.MkdirAll(configDir, 0755)
	_ = os.MkdirAll(libDir, 0755)
	initModelConfigsDir()
}

func findFreePort() int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 18080
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func main() {
	runtime.LockOSThread()

	port := findFreePort()

	mux := setupRoutes()
	sub, _ := fs.Sub(uiFiles, "ui")
	mux.Handle("/", http.FileServer(http.FS(sub)))

	go func() {
		addr := fmt.Sprintf("127.0.0.1:%d", port)
		if err := http.ListenAndServe(addr, mux); err != nil {
			log.Fatal(err)
		}
	}()

	w := newWebView(false)
	defer w.Destroy()
	defer svc.Stop()

	// Regenerate presets.ini on startup so it always reflects the current
	// models directory, even if the directory contents have changed offline.
	go func() {
		if _, err := writePresetsINI(); err != nil {
			globalLogHub.Publish(fmt.Sprintf("[%s] WRN presets.ini init: %v", ts(), err))
		}
	}()

	w.SetTitle("LLaMA Runner")
	w.SetSize(1680, 980, HintMin)
	w.Navigate(fmt.Sprintf("http://127.0.0.1:%d/", port))
	w.Run()
}
