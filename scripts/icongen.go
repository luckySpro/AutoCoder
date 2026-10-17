//go:build ignore

// 生成应用图标的源图（1024px PNG）：go run scripts/icongen.go <输出文件>
// 再用 Tauri 生成各平台图标：npx --prefix desktop tauri icon <输出文件> -o desktop/src-tauri/icons
package main

import (
	"fmt"
	"os"

	"github.com/luckySpro/AutoCoder/internal/tray"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "用法：go run scripts/icongen.go <输出文件>")
		os.Exit(2)
	}
	png := tray.Icon(tray.IconState{Slots: 3, Busy: 2}, 1024, false)
	if err := os.WriteFile(os.Args[1], png, 0o644); err != nil {
		panic(err)
	}
}
