//go:build windows

package main

import "github.com/jchv/go-webview2"

type HintType = webview2.Hint

const HintMin = webview2.HintMin

// WebView is the cross-platform webview interface.
type WebView interface {
	Destroy()
	SetTitle(title string)
	SetSize(w, h int, hint HintType)
	Navigate(url string)
	Run()
}

func newWebView(debug bool) WebView {
	return webview2.New(debug)
}
