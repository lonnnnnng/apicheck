package main

import (
	"context"
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
		BackgroundColour: options.NewRGBA(0, 0, 0, 0),
		Mac: &mac.Options{
			// 作者 long: 这里不启用 NSVisualEffectView 材质层，否则无边框窗口四角会露出系统矩形底色。
			// 真正的透明和圆角裁剪由 darwin_window_style.go 在原生窗口创建后补齐。
			WindowIsTranslucent:  false,
			WebviewIsTransparent: true,
		},
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		OnStartup: app.startup,
		OnDomReady: func(ctx context.Context) {
			applyRoundedTransparentWindow(12)
		},
		Bind: []interface{}{
			app,
		},
	}); err != nil {
		panic(err)
	}
}
