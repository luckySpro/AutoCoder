//go:build tray

package main

import (
	"log"

	"github.com/luckySpro/AutoCoder/internal/server"
	"github.com/luckySpro/AutoCoder/internal/tray"
)

// 桌面版：菜单栏图标显示运行状态、等你处理的事和今天的用量；
// 关掉浏览器后继续在后台运行，从菜单栏退出。
const desktop = true

func runUI(srv *server.Server, base string, stop func()) {
	tray.Run(tray.App{
		Base:     base,
		Summary:  srv.Summary,
		SetPause: srv.SetPaused,
		Open: func(u string) {
			if err := tray.OpenURL(u); err != nil {
				log.Printf("打开浏览器失败：%v", err)
			}
		},
		Quit: stop,
	})
}
