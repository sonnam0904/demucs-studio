//go:build !linux

package main

// tuneWebKit is a no-op outside Linux; the WebView2 and WKWebView backends have
// no equivalent renderer problem.
func tuneWebKit() {}
