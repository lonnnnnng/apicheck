package main

import (
	"embed"
	"fmt"
	"io/fs"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
)

//go:embed frontend/*
var embeddedAssets embed.FS

func main() {
	assets, err := fs.Sub(embeddedAssets, "frontend")
	if err != nil {
		panic(fmt.Errorf("load desktop assets: %w", err))
	}

	app := NewApp()
	if err := wails.Run(&options.App{
		Title:            "API Checker",
		Width:            1240,
		Height:           688,
		MinWidth:         980,
		MinHeight:        656,
		Frameless:        true,
		CSSDragProperty:  "--wails-draggable",
		CSSDragValue:     "drag",
		BackgroundColour: options.NewRGBA(246, 247, 249, 255),
		Mac: &mac.Options{
			// 作者 long: 透明 WebView 在圆角边缘容易被系统合成成黑边，桌面工具用浅色原生底色保持圆角观感更稳定。
			WindowIsTranslucent:  false,
			WebviewIsTransparent: false,
		},
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		OnStartup: app.startup,
		Bind: []interface{}{
			app,
		},
	}); err != nil {
		panic(err)
	}
}
