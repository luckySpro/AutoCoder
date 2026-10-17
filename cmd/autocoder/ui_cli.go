//go:build !tray

package main

import "github.com/luckySpro/AutoCoder/internal/server"

// 命令行版没有菜单栏图标，在前台运行，Ctrl+C 退出。
const desktop = false

func runUI(_ *server.Server, _ string, _ func()) { select {} }
