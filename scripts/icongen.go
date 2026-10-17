//go:build ignore

// 生成应用图标 PNG：go run scripts/icongen.go <输出目录>
// 输出 macOS iconset 需要的各个尺寸，以及 autocoder.png（256px，给 Linux 桌面文件用）。
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/luckySpro/AutoCoder/internal/tray"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "用法：go run scripts/icongen.go <输出目录>")
		os.Exit(2)
	}
	dir := os.Args[1]
	if err := os.MkdirAll(dir, 0o755); err != nil {
		panic(err)
	}
	st := tray.IconState{Slots: 3, Busy: 2}
	write := func(name string, size int) {
		if err := os.WriteFile(filepath.Join(dir, name), tray.Icon(st, size, false), 0o644); err != nil {
			panic(err)
		}
	}
	for _, s := range []int{16, 32, 128, 256, 512} {
		write(fmt.Sprintf("icon_%dx%d.png", s, s), s)
		write(fmt.Sprintf("icon_%dx%d@2x.png", s, s), s*2)
	}
	write("autocoder.png", 256)
}
