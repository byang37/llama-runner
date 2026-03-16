module llama-runner

go 1.21

require (
	github.com/jchv/go-webview2 v0.0.0-20260205173254-56598839c808 // Windows: WebView2-based webview
	github.com/webview/webview_go v0.0.0-20240831120633-6173450d4dd6 // macOS/Linux: native webview
)

require golang.org/x/sys v0.0.0-20210218145245-beda7e5e158e

require github.com/jchv/go-winloader v0.0.0-20250406163304-c1995be93bd1 // indirect
