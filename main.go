// DemucsStudio — tải audio từ YouTube rồi tách vocal bằng Demucs (htdemucs_ft)
// hoặc BS-RoFormer / Mel-Band RoFormer.
package main

import (
	"embed"
	"log"
	"runtime"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/linux"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed build/appicon.png
var icon []byte

// appVersion is overridden at build time with
// -ldflags "-X main.appVersion=x.y.z".
var appVersion = "dev"

const isWindows = runtime.GOOS == "windows"

func main() {
	// Must run before GTK/WebKit initialises.
	tuneWebKit()

	media, err := newMediaServer()
	if err != nil {
		log.Fatalf("media: %v", err)
	}
	app := NewApp(media)

	err = wails.Run(&options.App{
		Title:       "Demucs Studio",
		Width:       1180,
		Height:      820,
		MinWidth:    920,
		MinHeight:   640,
		AssetServer: &assetserver.Options{Assets: assets},
		// This colour is the stylesheet's --bg, so the window paints as the
		// app's own background from the first frame and the webview fills in
		// over it. That is what makes StartHidden unnecessary.
		//
		// StartHidden was used here once, with OnDomReady revealing the window.
		// It cost us a window that never appeared at all: Wails' Linux backend
		// hides the window from a g_idle_add callback (window.go:321 in v2.13),
		// and WindowShow queues onto the same idle list, so whichever call is
		// queued last wins. Lose that race and the app runs headless forever —
		// process alive, window created, Map State: IsUnMapped. A brief flash
		// of the background colour is a far better failure mode.
		OnStartup: app.startup,
		Bind:      []any{app},
		Linux: &linux.Options{
			Icon:                icon,
			WindowIsTranslucent: false,
			ProgramName:         "DemucsStudio",
		},
		Windows: &windows.Options{
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
		},
	})
	if err != nil {
		log.Fatalf("wails: %v", err)
	}
}
