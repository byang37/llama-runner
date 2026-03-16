//go:build darwin

package main

import "github.com/webview/webview_go"

type HintType = webview.Hint

const HintMin = webview.HintMin

// WebView is the cross-platform webview interface.
type WebView interface {
	Destroy()
	SetTitle(title string)
	SetSize(w, h int, hint HintType)
	Navigate(url string)
	Run()
}

func newWebView(debug bool) WebView {
	return webview.New(debug)
}
