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
		Title:            "Demucs Studio",
		Width:            1180,
		Height:           820,
		MinWidth:         920,
		MinHeight:        640,
		AssetServer:      &assetserver.Options{Assets: assets},
		BackgroundColour: &options.RGBA{R: 12, G: 14, B: 20, A: 1},
		// Hidden until the stylesheet has been applied; App.domReady shows it.
		StartHidden: true,
		OnStartup:   app.startup,
		OnDomReady:  app.domReady,
		Bind:        []any{app},
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
