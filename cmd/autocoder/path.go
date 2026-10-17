package main

import (
	"context"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// loginPath 让从访达、启动台或桌面菜单打开的桌面版也能找到开发工具：
// 这样启动的程序只有系统默认的 PATH，没有 Homebrew、npm、nvm 等目录。
// 这里读一次登录 shell 的 PATH，和当前的合并。
func loginPath() {
	if runtime.GOOS == "windows" {
		return
	}
	sh := os.Getenv("SHELL")
	if sh == "" {
		sh = "/bin/zsh"
		if runtime.GOOS != "darwin" {
			sh = "/bin/sh"
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	const mark = "__AUTOCODER_PATH__"
	out, err := exec.CommandContext(ctx, sh, "-ilc", "echo "+mark+"$PATH"+mark).Output()
	if err != nil {
		log.Printf("读取登录 shell 的 PATH 失败：%v", err)
	}
	var login string
	if parts := strings.Split(string(out), mark); len(parts) >= 3 {
		login = parts[1]
	}
	home, _ := os.UserHomeDir()
	extra := []string{"/opt/homebrew/bin", "/usr/local/bin", filepath.Join(home, ".local", "bin"), filepath.Join(home, ".npm-global", "bin"), filepath.Join(home, ".bun", "bin")}
	os.Setenv("PATH", mergePath(login, os.Getenv("PATH"), strings.Join(extra, string(os.PathListSeparator))))
}

// mergePath 按先后顺序合并多个 PATH，去掉重复和空项。
func mergePath(paths ...string) string {
	seen := map[string]bool{}
	var out []string
	for _, p := range paths {
		for _, d := range filepath.SplitList(p) {
			if d != "" && !seen[d] {
				seen[d] = true
				out = append(out, d)
			}
		}
	}
	return strings.Join(out, string(os.PathListSeparator))
}
