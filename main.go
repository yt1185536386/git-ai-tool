package main

import (
	"embed"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

// go:embed 嵌入前端构建产物（frontend/dist），打包后无需分发静态资源
//
//go:embed all:frontend/dist
var assets embed.FS

func main() {
	// 创建应用实例，核心逻辑见 app.go
	app := NewApp()

	// Wails 程序入口：配置窗口参数并绑定 App 结构体，
	// 绑定后其公开方法即可在前端通过 window.go.main.App.XXX() 调用
	err := wails.Run(&options.App{
		Title:     "Git AI Tool",
		Width:     1280,
		Height:    860,
		MinWidth:  960,
		MinHeight: 640,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 245, G: 247, B: 250, A: 1},
		OnStartup:        app.startup,
		Bind: []interface{}{
			app,
		},
		Windows: &windows.Options{
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
		},
	})
	if err != nil {
		println("Error:", err.Error())
	}
}
